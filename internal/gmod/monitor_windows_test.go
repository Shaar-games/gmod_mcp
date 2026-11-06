//go:build windows && 386

package gmod

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

type fakeMonitoredProcess struct {
	exited        bool
	code          uint32
	exitErr       error
	consoleErr    error
	closed        int
	ready         bool
	responsive    bool
	responseKnown bool
	responseErr   error
	readyErr      error
}

func (p *fakeMonitoredProcess) Ready() (bool, error) { return p.ready, p.readyErr }
func (p *fakeMonitoredProcess) Responsiveness() (bool, bool, error) {
	return p.responsive, p.responseKnown, p.responseErr
}

func (p *fakeMonitoredProcess) ExitCode() (uint32, bool, error) { return p.code, p.exited, p.exitErr }
func (p *fakeMonitoredProcess) Console() ([]string, error) {
	return []string{"last console line"}, p.consoleErr
}
func (p *fakeMonitoredProcess) Close() error { p.closed++; return nil }

func TestWatchCleanAndAbnormalExits(t *testing.T) {
	for _, code := range []uint32{0, 1, 0xc0000005, 259} {
		t.Run(strconv.FormatUint(uint64(code), 16), func(t *testing.T) {
			now := time.Now().UTC()
			process := &fakeMonitoredProcess{code: code}
			watch := &gameWatch{process: process, gameDir: t.TempDir(), event: GameEvent{PID: 123}}
			if _, done := watch.poll(now); done {
				t.Fatal("live process emitted an exit")
			}
			// A failed enumeration/query must never produce a false exit.
			process.exitErr = errors.New("access denied")
			if _, done := watch.poll(now.Add(time.Second)); done || process.closed != 0 {
				t.Fatal("query failure was classified as an exit")
			}
			process.exitErr = nil
			process.consoleErr = errors.New("process memory unavailable")
			watch.poll(now.Add(2 * time.Second))
			process.exited = true
			exitedAt := now.Add(3 * time.Second)
			if _, done := watch.poll(exitedAt); done {
				t.Fatal("did not wait for crash reports")
			}
			event, done := watch.poll(exitedAt.Add(crashFileGracePeriod))
			if !done || event.PID != 123 || event.ExitCode != code || process.closed != 1 {
				t.Fatalf("invalid exit: %+v, done=%t, closes=%d", event, done, process.closed)
			}
			if code == 0 {
				if event.Type != "game_stopped" || event.Console != "" {
					t.Fatalf("invalid clean exit: %+v", event)
				}
			} else if event.Type != "game_crashed" || event.Console != "last console line" || event.ConsoleCapturedAt == nil || !event.ConsoleCapturedAt.Equal(now) || event.ConsoleError == "" {
				t.Fatalf("last successful snapshot was lost: %+v", event)
			}
		})
	}
}

func TestWatchDetectsDelayedReportDespiteZeroExitCode(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "old.mdmp"), "old crash")
	baseline, _ := snapshotCrashFiles(dir)
	process := &fakeMonitoredProcess{exited: true}
	watch := &gameWatch{process: process, gameDir: dir, baseline: baseline}
	now := time.Now().UTC()
	watch.poll(now)
	writeTestFile(t, filepath.Join(dir, "new_crash.log"), "fatal error")
	event, done := watch.poll(now.Add(crashFileGracePeriod))
	if !done || event.Type != "game_crashed" || len(event.CrashFiles) != 1 || event.CrashFiles[0].Text != "fatal error" {
		t.Fatalf("delayed report not attached: %+v", event)
	}
}

func TestWatchDoesNotReuseStaleReports(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "old.mdmp"), "old crash")
	baseline, _ := snapshotCrashFiles(dir)
	watch := &gameWatch{process: &fakeMonitoredProcess{exited: true}, gameDir: dir, baseline: baseline}
	now := time.Now().UTC()
	watch.poll(now)
	event, done := watch.poll(now.Add(crashFileGracePeriod))
	if !done || event.Type != "game_stopped" || len(event.CrashFiles) != 0 {
		t.Fatalf("stale report caused a false crash: %+v", event)
	}
}

func TestReadyAndResponsivenessTransitions(t *testing.T) {
	process := &fakeMonitoredProcess{responseKnown: true, responsive: true}
	var events []GameEvent
	watch := &gameWatch{process: process, event: GameEvent{PID: 42}, emit: func(event GameEvent) { events = append(events, event) }}
	now := time.Now().UTC()
	watch.observeHealth(now)
	if len(events) != 0 {
		t.Fatal("game ready while engine is still loading")
	}
	process.ready, process.readyErr = true, errors.New("engine probe unavailable")
	watch.observeHealth(now)
	if len(events) != 0 {
		t.Fatal("game ready despite a failed engine probe")
	}
	process.readyErr = nil
	watch.observeHealth(now)
	watch.observeHealth(now.Add(time.Second))
	if len(events) != 1 || events[0].Type != "game_ready" || events[0].PID != 42 {
		t.Fatalf("readiness must fire once: %v", events)
	}
	process.responsive = false
	hungAt := now.Add(2 * time.Second)
	watch.observeHealth(hungAt)
	watch.observeHealth(hungAt.Add(unresponsiveThreshold - time.Millisecond))
	if len(events) != 1 {
		t.Fatal("short pause was reported as unresponsive")
	}
	watch.observeHealth(hungAt.Add(unresponsiveThreshold))
	watch.observeHealth(hungAt.Add(unresponsiveThreshold + time.Second))
	if len(events) != 2 || events[1].Type != "game_unresponsive" || events[1].UnresponsiveMilliseconds != unresponsiveThreshold.Milliseconds() {
		t.Fatalf("unresponsive transition should fire once after threshold: %v", events)
	}
	process.responseKnown = false
	watch.observeHealth(hungAt.Add(12 * time.Second))
	if len(events) != 2 {
		t.Fatal("missing window must not imply recovery")
	}
	process.responseKnown, process.responsive = true, true
	watch.observeHealth(hungAt.Add(13 * time.Second))
	watch.observeHealth(hungAt.Add(14 * time.Second))
	if len(events) != 3 || events[2].Type != "game_responsive" || events[2].UnresponsiveMilliseconds != 13000 || !events[2].UnresponsiveSince.Equal(hungAt) {
		t.Fatalf("recovery must fire once with the original hang duration: %v", events)
	}
}

func TestResponsivenessRequiresContinuousKnownFailure(t *testing.T) {
	process := &fakeMonitoredProcess{responseKnown: true}
	var events []GameEvent
	watch := &gameWatch{process: process, emit: func(event GameEvent) { events = append(events, event) }}
	now := time.Now().UTC()
	watch.observeHealth(now)
	process.responseErr = errors.New("access denied")
	watch.observeHealth(now.Add(9 * time.Second))
	process.responseErr = nil
	watch.observeHealth(now.Add(10 * time.Second))
	watch.observeHealth(now.Add(19 * time.Second))
	if len(events) != 0 {
		t.Fatal("probe error must reset the candidate hang interval")
	}
	process.responsive = true
	watch.observeHealth(now.Add(20 * time.Second))
	if len(events) != 0 {
		t.Fatal("short freeze must not produce a responsive event")
	}
	// A crash after a reported hang produces an exit, never a fake recovery.
	process.responsive = false
	watch.observeHealth(now.Add(21 * time.Second))
	watch.observeHealth(now.Add(31 * time.Second))
	process.exited = true
	watch.gameDir = t.TempDir()
	watch.poll(now.Add(32 * time.Second))
	if len(events) != 1 || events[0].Type != "game_unresponsive" {
		t.Fatalf("exit produced an unexpected health event: %v", events)
	}
}

// Exercise the real Windows process handle API without launching/crashing GMod.
func TestWindowsExitCode(t *testing.T) {
	if code := os.Getenv("GMOD_MCP_TEST_EXIT_CODE"); code != "" {
		_, _ = io.CopyN(io.Discard, os.Stdin, 1)
		value, _ := strconv.Atoi(code)
		os.Exit(value)
	}
	for _, exitCode := range []int{0, 259, -1073741819} {
		t.Run(strconv.Itoa(exitCode), func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestWindowsExitCode$")
			command.Env = append(os.Environ(), "GMOD_MCP_TEST_EXIT_CODE="+strconv.Itoa(exitCode))
			stdin, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer command.Process.Kill()
			handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(command.Process.Pid))
			if err != nil {
				t.Fatal(err)
			}
			process := &windowsMonitoredProcess{handle: handle}
			defer process.Close()
			if _, exited, err := process.ExitCode(); err != nil || exited {
				t.Fatalf("live process: exited=%t err=%v", exited, err)
			}
			if _, err := stdin.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			code, exited, err := process.ExitCode()
			if err != nil || !exited || code != uint32(exitCode) {
				t.Fatalf("exit code=%#x exited=%t err=%v; expected %#x", code, exited, err, uint32(exitCode))
			}
		})
	}
}
