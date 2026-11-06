//go:build windows && 386

package gmod

import (
	"context"
	"encoding/binary"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestRemoteCallTimeoutRetainsResourcesUntilCompletion(t *testing.T) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(event)
	defer windows.SetEvent(event)
	// WaitForSingleObject(event, INFINITE), executed in this test process.
	stub := []byte{0x68, 0xff, 0xff, 0xff, 0xff, 0x68, 0, 0, 0, 0, 0xb8, 0, 0, 0, 0, 0xff, 0xd0, 0xc2, 4, 0}
	binary.LittleEndian.PutUint32(stub[6:10], uint32(event))
	binary.LittleEndian.PutUint32(stub[11:15], uint32(kernel32.NewProc("WaitForSingleObject").Addr()))
	pid := windows.GetCurrentProcessId()
	if _, err := runRemoteStub(windows.CurrentProcess(), pid, stub, nil); err == nil {
		t.Fatal("blocked native call did not time out")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if release, err := nativeCallGate.acquire(ctx, pid); err == nil {
		release()
		t.Fatal("timed-out native thread released resources before finishing")
	}
	if err := windows.SetEvent(event); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	release, err := nativeCallGate.acquire(ctx2, pid)
	if err != nil {
		t.Fatalf("native call did not clean up after finishing: %v", err)
	}
	release()
	// mov eax, 42; ret 4: subsequent calls remain usable after cleanup.
	value, err := runRemoteStub(windows.CurrentProcess(), pid, []byte{0xb8, 42, 0, 0, 0, 0xc2, 4, 0}, nil)
	if err != nil || value != 42 {
		t.Fatalf("native return value=%d err=%v", value, err)
	}
}

func TestWindowProbeDetectsBlockedAndResponsiveMessageLoop(t *testing.T) {
	if responsive, known, err := probeWindow(0); err != nil || responsive || known {
		t.Fatalf("missing window must be unknown: responsive=%t known=%t err=%v", responsive, known, err)
	}
	startPump := make(chan struct{})
	windowReady := make(chan uintptr, 1)
	windowDone := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(windowDone)
		class, _ := windows.UTF16PtrFromString("STATIC")
		window, _, _ := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 1, 1, 0, 0, 0, 0)
		windowReady <- window
		if window == 0 {
			return
		}
		defer user32.NewProc("DestroyWindow").Call(window)
		<-startPump // simulate a blocked UI thread before pumping messages
		// Allocate enough space for MSG for either native alignment; tests are x86.
		var message [64]byte
		getMessage := user32.NewProc("GetMessageW")
		for {
			value, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&message[0])), 0, 0, 0)
			if value == 0 || int32(value) == -1 {
				return
			}
			user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&message[0])))
		}
	}()
	window := <-windowReady
	if window == 0 {
		close(startPump)
		t.Fatal("could not create test window")
	}
	defer func() {
		user32.NewProc("PostMessageW").Call(window, 0x12, 0, 0) // WM_QUIT
		select {
		case <-windowDone:
		case <-time.After(2 * time.Second):
			t.Error("test window did not shut down")
		}
	}()
	responsive, known, err := probeWindow(window)
	close(startPump)
	if err != nil || responsive || !known {
		t.Fatalf("blocked window: responsive=%t known=%t err=%v", responsive, known, err)
	}
	responsive, known, err = probeWindow(window)
	if err != nil || !responsive || !known {
		t.Fatalf("responsive window: responsive=%t known=%t err=%v", responsive, known, err)
	}
}
