//go:build windows && 386

package gmod

import (
	"context"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const remoteCallTimeout = 2 * time.Second

var nativeCallGate processGate

// runRemoteStub owns all native-call resources. A timed-out thread may still
// execute, so its code/data must remain allocated until it finishes. Keeping the
// per-process gate until then also prevents a stuck game accumulating threads.
func runRemoteStub(handle windows.Handle, pid uint32, stub, data []byte) (uintptr, error) {
	ctx, cancel := context.WithTimeout(context.Background(), remoteCallTimeout)
	defer cancel()
	release, err := nativeCallGate.acquire(ctx, pid)
	if err != nil {
		return 0, fmt.Errorf("previous engine call is still running: %w", err)
	}
	current := windows.CurrentProcess()
	var owned windows.Handle
	if err := windows.DuplicateHandle(current, handle, current, &owned, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		release()
		return 0, fmt.Errorf("retain process handle: %w", err)
	}
	var remoteCode, remoteData uintptr
	var thread windows.Handle
	cleanup := func() {
		if thread != 0 {
			_ = windows.CloseHandle(thread)
		}
		if remoteData != 0 {
			freeRemote(owned, remoteData)
		}
		if remoteCode != 0 {
			freeRemote(owned, remoteCode)
		}
		_ = windows.CloseHandle(owned)
		release()
	}
	remoteCode, err = allocRemote(owned, uintptr(len(stub)), pageExecuteReadwrite)
	if err != nil {
		cleanup()
		return 0, err
	}
	if err = writeRemote(owned, remoteCode, stub); err != nil {
		cleanup()
		return 0, err
	}
	if len(data) > 0 {
		remoteData, err = allocRemote(owned, uintptr(len(data)), pageReadwrite)
		if err == nil {
			err = writeRemote(owned, remoteData, data)
		}
		if err != nil {
			cleanup()
			return 0, err
		}
	}
	value, _, callErr := procCreateRemoteThread.Call(uintptr(owned), 0, 0, remoteCode, remoteData, 0, 0)
	if value == 0 {
		cleanup()
		return 0, fmt.Errorf("CreateRemoteThread: %v", callErr)
	}
	thread = windows.Handle(value)
	state, err := windows.WaitForSingleObject(thread, uint32(remoteCallTimeout/time.Millisecond))
	if err != nil || state != windows.WAIT_OBJECT_0 {
		go func() {
			// The duplicated process handle survives GMod.Close. Never free
			// executable memory while the target thread might still be using it.
			if state, err := windows.WaitForSingleObject(thread, infinite); err == nil && state == windows.WAIT_OBJECT_0 {
				cleanup()
			}
		}()
		if err != nil {
			return 0, fmt.Errorf("wait for engine call: %w", err)
		}
		return 0, fmt.Errorf("engine call did not finish within %s; execution is unconfirmed", remoteCallTimeout)
	}
	defer cleanup()
	var code uint32
	ok, _, callErr := procGetExitCodeThread.Call(uintptr(thread), uintptr(unsafe.Pointer(&code)))
	if ok == 0 {
		return 0, fmt.Errorf("GetExitCodeThread: %v", callErr)
	}
	return uintptr(code), nil
}
