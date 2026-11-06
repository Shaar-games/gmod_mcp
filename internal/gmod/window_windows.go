//go:build windows && 386

package gmod

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows        = user32.NewProc("EnumWindows")
	procGetWindowThreadPID = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible    = user32.NewProc("IsWindowVisible")
	procGetWindow          = user32.NewProc("GetWindow")
	procGetClassName       = user32.NewProc("GetClassNameW")
	procSendMessageTimeout = user32.NewProc("SendMessageTimeoutW")
	procSetLastError       = kernel32.NewProc("SetLastError")
	enumerateGameWindow    = windows.NewCallback(visitGameWindow)
	windowSearches         sync.Map
	windowSearchID         atomic.Uintptr
)

type windowSearch struct {
	pid    uint32
	window uintptr
}

func windowPID(window uintptr) uint32 {
	var pid uint32
	procGetWindowThreadPID.Call(window, uintptr(unsafe.Pointer(&pid)))
	return pid
}

func visitGameWindow(window, param uintptr) uintptr {
	value, found := windowSearches.Load(param)
	if !found {
		return 0
	}
	search := value.(*windowSearch)
	if windowPID(window) != search.pid {
		return 1
	}
	visible, _, _ := procIsWindowVisible.Call(window)
	owner, _, _ := procGetWindow.Call(window, 4) // GW_OWNER
	if visible == 0 || owner != 0 {
		return 1
	}
	if search.window == 0 {
		search.window = window
	}
	var class [256]uint16
	procGetClassName.Call(window, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
	if windows.UTF16ToString(class[:]) == "Valve001" {
		search.window = window
		return 0
	}
	return 1
}

func gameWindow(pid uint32) (uintptr, error) {
	search := &windowSearch{pid: pid}
	id := windowSearchID.Add(1)
	windowSearches.Store(id, search)
	defer windowSearches.Delete(id)
	ok, _, err := procEnumWindows.Call(enumerateGameWindow, id)
	// EnumWindows returns zero when our callback stops at the game window.
	if ok == 0 && search.window == 0 {
		return 0, fmt.Errorf("EnumWindows: %v", err)
	}
	return search.window, nil
}

// probeWindow uses a bounded WM_NULL round trip. An absent window or an access
// error is unknown, not an unresponsive game. No game state is changed.
func probeWindow(window uintptr) (responsive, known bool, err error) {
	if window == 0 {
		return false, false, nil
	}
	var result uintptr
	// Keep SetLastError and SendMessageTimeout on the same OS thread: a timeout
	// can return zero without setting last-error, per the Windows API contract.
	runtime.LockOSThread()
	procSetLastError.Call(0)
	ok, _, callErr := procSendMessageTimeout.Call(window, 0, 0, 0,
		0x1|0x2|0x20, 100, uintptr(unsafe.Pointer(&result))) // BLOCK | ABORTIFHUNG | ERRORONEXIT
	runtime.UnlockOSThread()
	if ok != 0 {
		return true, true, nil
	}
	if callErr == windows.ERROR_SUCCESS || callErr == windows.ERROR_TIMEOUT {
		return false, true, nil
	}
	return false, false, fmt.Errorf("window responsiveness probe: %v", callErr)
}
