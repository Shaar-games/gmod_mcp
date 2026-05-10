//go:build windows && 386

package gmod

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	th32csSnapProcess  = 0x00000002
	th32csSnapModule   = 0x00000008
	th32csSnapModule32 = 0x00000010
	infinite           = 0xFFFFFFFF
)

type processEntry32 struct {
	Size              uint32
	CntUsage          uint32
	ProcessID         uint32
	DefaultHeapID     uintptr
	ModuleID          uint32
	ThreadCnt         uint32
	ParentProcessID   uint32
	PriorityClassBase int32
	Flags             uint32
	ExeFile           [260]uint16
}

type moduleEntry32W struct {
	Size         uint32
	ModuleID     uint32
	ProcessID    uint32
	GlblCntUsage uint32
	ProccntUsage uint32
	ModBaseAddr  uintptr
	ModBaseSize  uint32
	HModule      uintptr
	SzModule     [256]uint16
	SzExePath    [260]uint16
}

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	procCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32First           = kernel32.NewProc("Process32FirstW")
	procProcess32Next            = kernel32.NewProc("Process32NextW")
	procModule32First            = kernel32.NewProc("Module32FirstW")
	procModule32Next             = kernel32.NewProc("Module32NextW")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
	procVirtualAllocEx           = kernel32.NewProc("VirtualAllocEx")
	procVirtualFreeEx            = kernel32.NewProc("VirtualFreeEx")
	procWriteProcessMemory       = kernel32.NewProc("WriteProcessMemory")
	procReadProcessMemory        = kernel32.NewProc("ReadProcessMemory")
	procCreateRemoteThread       = kernel32.NewProc("CreateRemoteThread")
	procWaitForSingleObject      = kernel32.NewProc("WaitForSingleObject")
	procGetExitCodeThread        = kernel32.NewProc("GetExitCodeThread")
	procShellExecute             = shell32.NewProc("ShellExecuteW")
)

const (
	memCommit            = 0x1000
	memReserve           = 0x2000
	memRelease           = 0x8000
	pageReadwrite        = 0x04
	pageExecuteReadwrite = 0x40
)

func processAccessMask() uint32 {
	return 0x0008 | 0x0010 | 0x0020 | 0x0002 | 0x0400 | 0x1000
}

func findAllProcessIDsByName(name string) ([]uint32, error) {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return nil, fmt.Errorf("empty process name")
	}

	snap, _, err := procCreateToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if snap == uintptr(windows.InvalidHandle) {
		return nil, fmt.Errorf("CreateToolhelp32Snapshot: %v", err)
	}
	defer procCloseHandle.Call(snap)

	var pe processEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	ok, _, _ := procProcess32First.Call(snap, uintptr(unsafe.Pointer(&pe)))
	if ok == 0 {
		return nil, fmt.Errorf("Process32First: %v", err)
	}

	var matches []uint32
	for {
		if strings.EqualFold(strings.TrimSpace(windows.UTF16ToString(pe.ExeFile[:])), want) {
			matches = append(matches, pe.ProcessID)
		}
		if ok, _, _ = procProcess32Next.Call(snap, uintptr(unsafe.Pointer(&pe))); ok == 0 {
			break
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("process %q not found", name)
	}
	return matches, nil
}

func findModuleBase(pid uint32, module string) (uintptr, error) {
	want := strings.ToLower(strings.TrimSpace(module))
	snap, _, err := procCreateToolhelp32Snapshot.Call(th32csSnapModule|th32csSnapModule32, uintptr(pid))
	if snap == uintptr(windows.InvalidHandle) {
		return 0, fmt.Errorf("CreateToolhelp32Snapshot(module): %v", err)
	}
	defer procCloseHandle.Call(snap)

	var me moduleEntry32W
	me.Size = uint32(unsafe.Sizeof(me))
	ok, _, _ := procModule32First.Call(snap, uintptr(unsafe.Pointer(&me)))
	if ok == 0 {
		return 0, fmt.Errorf("Module32First: %v", err)
	}
	for {
		if strings.ToLower(windows.UTF16ToString(me.SzModule[:])) == want {
			return me.ModBaseAddr, nil
		}
		if ok, _, _ = procModule32Next.Call(snap, uintptr(unsafe.Pointer(&me))); ok == 0 {
			break
		}
	}
	return 0, fmt.Errorf("module %q not loaded in pid %d", module, pid)
}

func openTargetProcess(pid uint32) (windows.Handle, error) {
	h, err := windows.OpenProcess(processAccessMask(), false, pid)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("OpenProcess: %w", err)
	}
	return h, nil
}

func readRemoteUint32(h windows.Handle, addr uintptr) (uint32, error) {
	var val uint32
	var nRead uintptr
	ok, _, _ := procReadProcessMemory.Call(
		uintptr(h), addr, uintptr(unsafe.Pointer(&val)), 4, uintptr(unsafe.Pointer(&nRead)),
	)
	if ok == 0 || nRead != 4 {
		return 0, fmt.Errorf("ReadProcessMemory(0x%X): nRead=%d", addr, nRead)
	}
	return val, nil
}

func readRemoteUint16(h windows.Handle, addr uintptr) (uint16, error) {
	var val uint16
	var nRead uintptr
	ok, _, _ := procReadProcessMemory.Call(
		uintptr(h), addr, uintptr(unsafe.Pointer(&val)), 2, uintptr(unsafe.Pointer(&nRead)),
	)
	if ok == 0 || nRead != 2 {
		return 0, fmt.Errorf("ReadProcessMemory(0x%X): nRead=%d", addr, nRead)
	}
	return val, nil
}

func readRemoteBytes(h windows.Handle, addr uintptr, size int) ([]byte, error) {
	if size <= 0 {
		return nil, fmt.Errorf("invalid size %d", size)
	}
	buf := make([]byte, size)
	var nRead uintptr
	ok, _, _ := procReadProcessMemory.Call(
		uintptr(h), addr, uintptr(unsafe.Pointer(&buf[0])), uintptr(size), uintptr(unsafe.Pointer(&nRead)),
	)
	if ok == 0 {
		return nil, fmt.Errorf("ReadProcessMemory(0x%X): failed", addr)
	}
	return buf[:nRead], nil
}

func readRemoteByte(h windows.Handle, addr uintptr) (byte, error) {
	var val byte
	var nRead uintptr
	ok, _, _ := procReadProcessMemory.Call(
		uintptr(h), addr, uintptr(unsafe.Pointer(&val)), 1, uintptr(unsafe.Pointer(&nRead)),
	)
	if ok == 0 || nRead != 1 {
		return 0, fmt.Errorf("ReadProcessMemory(0x%X): nRead=%d", addr, nRead)
	}
	return val, nil
}

// readRemoteCString reads a null-terminated string from the remote process (ASCII / UTF-8 safe byte-wise).
func readRemoteCString(h windows.Handle, addr uintptr, maxLen int) (string, error) {
	if addr == 0 || maxLen <= 0 {
		return "", nil
	}
	const chunk = 256
	var out []byte
	for len(out) < maxLen {
		n := chunk
		if maxLen-len(out) < n {
			n = maxLen - len(out)
		}
		part, err := readRemoteBytes(h, addr+uintptr(len(out)), n)
		if err != nil {
			b, byteErr := readRemoteByte(h, addr+uintptr(len(out)))
			if byteErr != nil {
				return "", err
			}
			if b == 0 {
				return string(out), nil
			}
			out = append(out, b)
			continue
		}
		for i, b := range part {
			if b == 0 {
				return string(append(out, part[:i]...)), nil
			}
		}
		out = append(out, part...)
		if len(part) < n {
			break
		}
	}
	return string(out), nil
}

func allocRemote(h windows.Handle, size uintptr, protect uint32) (uintptr, error) {
	addr, _, err := procVirtualAllocEx.Call(uintptr(h), 0, size, memCommit|memReserve, uintptr(protect))
	if addr == 0 {
		return 0, fmt.Errorf("VirtualAllocEx: %v", err)
	}
	return addr, nil
}

func writeRemote(h windows.Handle, addr uintptr, data []byte) error {
	var n uintptr
	ok, _, _ := procWriteProcessMemory.Call(
		uintptr(h), addr, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), uintptr(unsafe.Pointer(&n)),
	)
	if ok == 0 || n != uintptr(len(data)) {
		return fmt.Errorf("WriteProcessMemory: wrote %d/%d", n, len(data))
	}
	return nil
}

func freeRemote(h windows.Handle, addr uintptr) {
	procVirtualFreeEx.Call(uintptr(h), addr, 0, memRelease)
}

func remoteCall(h windows.Handle, fn uintptr, arg uintptr) (uintptr, error) {
	thread, _, err := procCreateRemoteThread.Call(uintptr(h), 0, 0, fn, arg, 0, 0)
	if thread == 0 {
		return 0, fmt.Errorf("CreateRemoteThread: %v", err)
	}
	defer procCloseHandle.Call(thread)

	procWaitForSingleObject.Call(thread, infinite)

	var exitCode uintptr
	procGetExitCodeThread.Call(thread, uintptr(unsafe.Pointer(&exitCode)))
	return exitCode, nil
}

func shellOpen(target string) error {
	operation, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	ret, _, callErr := procShellExecute.Call(0, uintptr(unsafe.Pointer(operation)), uintptr(unsafe.Pointer(file)), 0, 0, 1)
	if ret <= 32 {
		return fmt.Errorf("ShellExecuteW(%q): return=%d err=%v", target, ret, callErr)
	}
	return nil
}

// buildThiscallNoArgStub builds an x86 thiscall stub for bool-ish no-argument methods.
func buildThiscallNoArgStub(fn, thisPtr uintptr) []byte {
	stub := []byte{
		0xB9, 0x00, 0x00, 0x00, 0x00, // mov ecx, thisPtr
		0xB8, 0x00, 0x00, 0x00, 0x00, // mov eax, fn
		0xFF, 0xD0, // call eax
		0x0F, 0xB6, 0xC0, // movzx eax, al
		0xC2, 0x04, 0x00, // ret 4
	}
	binary.LittleEndian.PutUint32(stub[1:5], uint32(thisPtr))
	binary.LittleEndian.PutUint32(stub[6:10], uint32(fn))
	return stub
}

// buildClientCmdStub builds x86 thiscall stub:
//
//	mov ecx, <this>       ; thiscall: ECX = this
//	push [esp+4]          ; push cmd string (from CreateRemoteThread arg)
//	mov eax, <clientCmd>
//	call eax              ; callee does ret 4 (one stack arg)
//	ret 4                 ; clean CreateRemoteThread's arg
func buildClientCmdStub(clientCmd, thisPtr uintptr) []byte {
	stub := []byte{
		0xB9, 0x00, 0x00, 0x00, 0x00, // mov ecx, thisPtr
		0xFF, 0x74, 0x24, 0x04, // push [esp+4]
		0xB8, 0x00, 0x00, 0x00, 0x00, // mov eax, clientCmd
		0xFF, 0xD0, // call eax
		0xC2, 0x04, 0x00, // ret 4
	}
	binary.LittleEndian.PutUint32(stub[1:5], uint32(thisPtr))
	binary.LittleEndian.PutUint32(stub[10:14], uint32(clientCmd))
	return stub
}
