//go:build windows && 386

package gmod

import (
	"encoding/binary"
	"errors"
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
	waitObject0        = 0x00000000
	waitTimeout        = 0x00000102
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

type moduleInfo struct {
	Base uintptr
	Size uint32
}

type memoryBasicInformation struct {
	BaseAddress       uintptr
	AllocationBase    uintptr
	AllocationProtect uint32
	RegionSize        uintptr
	State             uint32
	Protect           uint32
	Type              uint32
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
	procVirtualQueryEx           = kernel32.NewProc("VirtualQueryEx")
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
	pageNoaccess         = 0x01
	pageReadonly         = 0x02
	pageWritecopy        = 0x08
	pageExecute          = 0x10
	pageExecuteRead      = 0x20
	pageExecuteWritecopy = 0x80
	pageGuard            = 0x100

	defaultRemoteCallTimeoutMS = 5000
)

var errRemoteThreadTimeout = errors.New("remote thread timed out")

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
	info, err := findModuleInfo(pid, module)
	if err != nil {
		return 0, err
	}
	return info.Base, nil
}

func findModuleInfo(pid uint32, module string) (moduleInfo, error) {
	want := strings.ToLower(strings.TrimSpace(module))
	snap, _, err := procCreateToolhelp32Snapshot.Call(th32csSnapModule|th32csSnapModule32, uintptr(pid))
	if snap == uintptr(windows.InvalidHandle) {
		return moduleInfo{}, fmt.Errorf("CreateToolhelp32Snapshot(module): %v", err)
	}
	defer procCloseHandle.Call(snap)

	var me moduleEntry32W
	me.Size = uint32(unsafe.Sizeof(me))
	ok, _, _ := procModule32First.Call(snap, uintptr(unsafe.Pointer(&me)))
	if ok == 0 {
		return moduleInfo{}, fmt.Errorf("Module32First: %v", err)
	}
	for {
		if strings.ToLower(windows.UTF16ToString(me.SzModule[:])) == want {
			return moduleInfo{Base: me.ModBaseAddr, Size: me.ModBaseSize}, nil
		}
		if ok, _, _ = procModule32Next.Call(snap, uintptr(unsafe.Pointer(&me))); ok == 0 {
			break
		}
	}
	return moduleInfo{}, fmt.Errorf("module %q not loaded in pid %d", module, pid)
}

func openTargetProcess(pid uint32) (windows.Handle, error) {
	h, err := windows.OpenProcess(processAccessMask(), false, pid)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("OpenProcess: %w", err)
	}
	return h, nil
}

func queryRemoteMemory(h windows.Handle, addr uintptr) (memoryBasicInformation, error) {
	var mbi memoryBasicInformation
	n, _, err := procVirtualQueryEx.Call(
		uintptr(h),
		addr,
		uintptr(unsafe.Pointer(&mbi)),
		unsafe.Sizeof(mbi),
	)
	if n == 0 {
		return memoryBasicInformation{}, fmt.Errorf("VirtualQueryEx(0x%X): %v", addr, err)
	}
	return mbi, nil
}

func remoteRangeAvailable(h windows.Handle, addr uintptr, size uintptr, executable bool) error {
	if addr == 0 {
		return fmt.Errorf("address is NULL")
	}
	if size == 0 {
		size = 1
	}

	mbi, err := queryRemoteMemory(h, addr)
	if err != nil {
		return err
	}
	if mbi.State != memCommit {
		return fmt.Errorf("address 0x%X is not committed memory", addr)
	}
	if mbi.Protect&pageGuard != 0 || mbi.Protect&pageNoaccess != 0 {
		return fmt.Errorf("address 0x%X has inaccessible protection 0x%X", addr, mbi.Protect)
	}
	if executable && !isExecutableProtect(mbi.Protect) {
		return fmt.Errorf("address 0x%X is not executable memory (protect=0x%X)", addr, mbi.Protect)
	}
	if !executable && !isReadableProtect(mbi.Protect) {
		return fmt.Errorf("address 0x%X is not readable memory (protect=0x%X)", addr, mbi.Protect)
	}

	offset := addr - mbi.BaseAddress
	if offset > mbi.RegionSize || size > mbi.RegionSize-offset {
		return fmt.Errorf("range 0x%X..0x%X crosses memory region 0x%X..0x%X",
			addr, addr+size, mbi.BaseAddress, mbi.BaseAddress+mbi.RegionSize)
	}
	return nil
}

func ensureRemoteReadable(h windows.Handle, addr uintptr, size uintptr) error {
	return remoteRangeAvailable(h, addr, size, false)
}

func ensureRemoteExecutable(h windows.Handle, addr uintptr) error {
	return remoteRangeAvailable(h, addr, 1, true)
}

func isReadableProtect(protect uint32) bool {
	return protect&(pageReadonly|pageReadwrite|pageWritecopy|pageExecuteRead|pageExecuteReadwrite|pageExecuteWritecopy) != 0
}

func isExecutableProtect(protect uint32) bool {
	return protect&(pageExecute|pageExecuteRead|pageExecuteReadwrite|pageExecuteWritecopy) != 0
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

func readRemoteUint32FromBytes(buf []byte, off int) (uint32, error) {
	if off < 0 || off+4 > len(buf) {
		return 0, fmt.Errorf("uint32 offset %d out of range", off)
	}
	return binary.LittleEndian.Uint32(buf[off : off+4]), nil
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

func readRemoteModuleBytes(h windows.Handle, base uintptr, size uint32) []byte {
	out := make([]byte, int(size))
	const chunk = 0x10000
	for off := 0; off < len(out); off += chunk {
		n := chunk
		if len(out)-off < n {
			n = len(out) - off
		}
		part, err := readRemoteBytes(h, base+uintptr(off), n)
		if err != nil {
			continue
		}
		copy(out[off:], part)
	}
	return out
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
	if err := ensureRemoteExecutable(h, fn); err != nil {
		return 0, fmt.Errorf("remote call target is unsafe: %w", err)
	}

	thread, _, err := procCreateRemoteThread.Call(uintptr(h), 0, 0, fn, arg, 0, 0)
	if thread == 0 {
		return 0, fmt.Errorf("CreateRemoteThread: %v", err)
	}
	defer procCloseHandle.Call(thread)

	wait, _, waitErr := procWaitForSingleObject.Call(thread, defaultRemoteCallTimeoutMS)
	if wait == waitTimeout {
		return 0, fmt.Errorf("%w after %dms", errRemoteThreadTimeout, defaultRemoteCallTimeoutMS)
	}
	if wait != waitObject0 {
		return 0, fmt.Errorf("WaitForSingleObject: return=%d err=%v", wait, waitErr)
	}

	var exitCode uintptr
	ok, _, exitErr := procGetExitCodeThread.Call(thread, uintptr(unsafe.Pointer(&exitCode)))
	if ok == 0 {
		return 0, fmt.Errorf("GetExitCodeThread: %v", exitErr)
	}
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

func findRemoteExport(h windows.Handle, moduleBase uintptr, exportName string) (uintptr, error) {
	mz, err := readRemoteUint16(h, moduleBase)
	if err != nil {
		return 0, err
	}
	if mz != 0x5A4D {
		return 0, fmt.Errorf("missing MZ header")
	}

	eLfanew, err := readRemoteUint32(h, moduleBase+0x3C)
	if err != nil {
		return 0, err
	}

	nt := moduleBase + uintptr(eLfanew)
	pe, err := readRemoteUint32(h, nt)
	if err != nil {
		return 0, err
	}
	if pe != 0x00004550 {
		return 0, fmt.Errorf("missing PE header")
	}

	optional := nt + 24
	exportRVA, err := readRemoteUint32(h, optional+96)
	if err != nil {
		return 0, err
	}
	if exportRVA == 0 {
		return 0, fmt.Errorf("module has no export table")
	}

	exportDir := moduleBase + uintptr(exportRVA)
	numberOfNames, err := readRemoteUint32(h, exportDir+24)
	if err != nil {
		return 0, err
	}
	functionsRVA, err := readRemoteUint32(h, exportDir+28)
	if err != nil {
		return 0, err
	}
	namesRVA, err := readRemoteUint32(h, exportDir+32)
	if err != nil {
		return 0, err
	}
	ordinalsRVA, err := readRemoteUint32(h, exportDir+36)
	if err != nil {
		return 0, err
	}

	for i := uint32(0); i < numberOfNames; i++ {
		nameRVA, err := readRemoteUint32(h, moduleBase+uintptr(namesRVA)+uintptr(i)*4)
		if err != nil {
			return 0, err
		}
		name, err := readRemoteCString(h, moduleBase+uintptr(nameRVA), 256)
		if err != nil {
			return 0, err
		}
		if name != exportName {
			continue
		}

		ordinal, err := readRemoteUint16(h, moduleBase+uintptr(ordinalsRVA)+uintptr(i)*2)
		if err != nil {
			return 0, err
		}
		functionRVA, err := readRemoteUint32(h, moduleBase+uintptr(functionsRVA)+uintptr(ordinal)*4)
		if err != nil {
			return 0, err
		}
		return moduleBase + uintptr(functionRVA), nil
	}

	return 0, fmt.Errorf("export %q not found", exportName)
}

func findConsoleGlobals(h windows.Handle, engineBase uintptr, engineSize uint32) (consoleGlobals, error) {
	module := readRemoteModuleBytes(h, engineBase, engineSize)
	if len(module) == 0 {
		return consoleGlobals{}, fmt.Errorf("engine.dll bytes are empty")
	}

	pattern := []byte{
		0x0F, 0xB7, 0x05, 0, 0, 0, 0, // movzx eax, word ptr [chain+2]
		0x53,
		0x8B, 0x5D, 0x08,
		0x57,
		0x8B, 0x7D, 0x0C,
		0x4F,
		0x3D, 0xFF, 0xFF, 0x00, 0x00,
		0x74, 0,
		0x8B, 0x0D, 0, 0, 0, 0, // mov ecx, [nodes]
	}
	mask := "xxx????xxxxxxxxxxxxxxx?xx????"

	off := indexPattern(module, pattern, mask)
	if off < 0 {
		return consoleGlobals{}, fmt.Errorf("engine console globals signature not found")
	}

	chainPlus2, err := readRemoteUint32FromBytes(module, off+3)
	if err != nil {
		return consoleGlobals{}, err
	}
	nodesPtr, err := readRemoteUint32FromBytes(module, off+25)
	if err != nil {
		return consoleGlobals{}, err
	}

	if chainPlus2 < 2 {
		return consoleGlobals{}, fmt.Errorf("invalid console chain address 0x%X", chainPlus2)
	}
	return consoleGlobals{
		nodesPtrAddr:  uintptr(nodesPtr),
		chainWordAddr: uintptr(chainPlus2 - 2),
	}, nil
}

func indexPattern(buf []byte, pattern []byte, mask string) int {
	if len(pattern) != len(mask) || len(pattern) == 0 || len(buf) < len(pattern) {
		return -1
	}
	for i := 0; i <= len(buf)-len(pattern); i++ {
		matched := true
		for j := range pattern {
			if mask[j] == 'x' && buf[i+j] != pattern[j] {
				matched = false
				break
			}
		}
		if matched {
			return i
		}
	}
	return -1
}

func buildCreateInterfaceStub(createInterface uintptr) []byte {
	stub := []byte{
		0x6A, 0x00, // push 0
		0xFF, 0x74, 0x24, 0x08, // push [esp+8] ; original thread arg after push 0
		0xB8, 0x00, 0x00, 0x00, 0x00, // mov eax, createInterface
		0xFF, 0xD0, // call eax
		0x83, 0xC4, 0x08, // add esp, 8
		0xC2, 0x04, 0x00, // ret 4
	}
	binary.LittleEndian.PutUint32(stub[7:11], uint32(createInterface))
	return stub
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
