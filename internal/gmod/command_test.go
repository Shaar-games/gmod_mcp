package gmod

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeCommandTarget struct {
	pid         uint32
	mu          sync.Mutex
	begin, end  string
	output      []string
	confirm     bool
	readErr     error
	dispatchErr error
	reads       int
	dispatched  int
	dispatch    func(string)
	read        func(int, string, string) ([]string, error)
}

func (f *fakeCommandTarget) PID() uint32 { return f.pid }
func (f *fakeCommandTarget) RunConsoleCommand(command string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatched++
	lines := strings.Split(command, "\n")
	f.begin = strings.TrimPrefix(lines[0], "echo ")
	f.end = strings.TrimPrefix(lines[len(lines)-2], "echo ")
	if f.dispatch != nil {
		f.dispatch(command)
	}
	return f.dispatchErr
}
func (f *fakeCommandTarget) ReadRecentConsoleLines(int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.read != nil {
		return f.read(f.reads, f.begin, f.end)
	}
	lines := append([]string{"unrelated old output", f.begin}, f.output...)
	if f.confirm {
		lines = append(lines, f.end)
	}
	return lines, f.readErr
}

func TestCommandConfirmsEngineExecutionAndCapturesErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		output []string
		status string
	}{
		{"printed output", []string{"message"}, "executed"},
		{"silent command", nil, "executed"},
		{"unknown command", []string{`2852(286.3): Unknown command "invalid"`}, "error"},
		{"Lua error", []string{"[ERROR] lua/example.lua:4: failure", " 1. error - [C]:-1"}, "error"},
		{"cheat rejected", []string{"Can't use cheat cvar test in multiplayer, unless the server has sv_cheats set to 1."}, "error"},
		{"ordinary error word", []string{"There are 0 errors"}, "executed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := &fakeCommandTarget{confirm: true, output: test.output}
			result, err := executeConsoleCommand(context.Background(), target, "test", time.Second)
			if err != nil || result.Status != test.status || !result.ExecutionConfirmed || result.TimedOut {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if strings.Contains(result.Output, "__gmod_mcp_") || strings.Contains(result.Output, "unrelated old output") {
				t.Fatalf("capture leaked old output or markers: %s", result.Output)
			}
			if (len(result.Errors) > 0) != (test.status == "error") {
				t.Fatalf("incorrect errors: %v", result.Errors)
			}
		})
	}
}

func TestCommandDelayedMarkersAndAsynchronousOutput(t *testing.T) {
	target := &fakeCommandTarget{read: func(n int, begin, end string) ([]string, error) {
		switch n {
		case 1:
			return []string{"old output"}, nil
		case 2:
			return []string{begin, "working"}, nil
		default:
			return []string{begin, "working", end, "[ERROR] delayed server reply"}, nil
		}
	}}
	result, err := executeConsoleCommand(context.Background(), target, "test", time.Second)
	if err != nil || result.Status != "error" || !result.ExecutionConfirmed || !strings.Contains(result.Output, "delayed server reply") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCommandCannotReportSuccessForSubmissionAlone(t *testing.T) {
	for _, test := range []struct {
		name   string
		target *fakeCommandTarget
	}{
		{"missing end marker", &fakeCommandTarget{}},
		{"console inaccessible", &fakeCommandTarget{confirm: true, readErr: errors.New("memory unavailable")}},
		{"marker embedded in text", &fakeCommandTarget{read: func(_ int, begin, end string) ([]string, error) {
			return []string{"echo " + begin, "echo " + end}, nil
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := executeConsoleCommand(context.Background(), test.target, "test", 100*time.Millisecond)
			if err != nil || result.Status != "unconfirmed" || result.ExecutionConfirmed || !result.TimedOut || result.CaptureError == "" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
	result, err := executeConsoleCommand(context.Background(), &fakeCommandTarget{dispatchErr: errors.New("dispatch failed")}, "test", time.Second)
	if err == nil || result.Status != "unconfirmed" || result.ExecutionConfirmed {
		t.Fatalf("submission failure: %+v, err=%v", result, err)
	}
}

func TestCommandHistoryEvictionAndByteLimit(t *testing.T) {
	target := &fakeCommandTarget{read: func(_ int, _, end string) ([]string, error) {
		return []string{"tail of command output", end}, nil
	}}
	result, err := executeConsoleCommand(context.Background(), target, "test", time.Second)
	if err != nil || result.Status != "unconfirmed" || !result.ExecutionConfirmed || !result.OutputTruncated {
		t.Fatalf("evicted history must not imply success: %+v err=%v", result, err)
	}
	target = &fakeCommandTarget{confirm: true, output: []string{strings.Repeat("x", maxReportBytes+100)}}
	result, err = executeConsoleCommand(context.Background(), target, "test", time.Second)
	if err != nil || result.Status != "executed" || !result.OutputTruncated || len(result.Output) != maxReportBytes {
		t.Fatalf("byte limit: status=%s bytes=%d truncated=%t err=%v", result.Status, len(result.Output), result.OutputTruncated, err)
	}
}

func TestCommandMarkersSupportRealEngineHistoryFormat(t *testing.T) {
	frame, started, ended := commandFrame([]string{
		"2851(290.000100):  __begin \n",
		"2852(290.000120):  Unknown command \"invalid\"\n",
		"2853(290.000130):  __end \n",
	}, "__begin", "__end")
	if !started || !ended || len(frame) != 1 || len(commandErrors(frame)) != 1 {
		t.Fatalf("timestamped history: %v started=%t ended=%t", frame, started, ended)
	}
}

func TestConcurrentCommandsAreSerializedPerProcess(t *testing.T) {
	var calls sync.Mutex
	var dispatchTimes []time.Time
	target := &fakeCommandTarget{pid: 42, confirm: true}
	target.dispatch = func(string) {
		calls.Lock()
		defer calls.Unlock()
		dispatchTimes = append(dispatchTimes, time.Now())
	}
	var group sync.WaitGroup
	for i := range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := executeConsoleCommand(context.Background(), target, fmt.Sprintf("test%d", i), 2*time.Second)
			if err != nil || result.Status != "executed" {
				t.Errorf("result=%+v err=%v", result, err)
			}
		}()
	}
	group.Wait()
	if len(dispatchTimes) != 2 || dispatchTimes[1].Sub(dispatchTimes[0]) < commandSettleTime {
		t.Fatalf("commands were submitted before previous capture finished: %v", dispatchTimes)
	}
}

func TestProcessGateCancellationAndCleanup(t *testing.T) {
	var gate processGate
	release, err := gate.acquire(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gate.acquire(ctx, 42); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	release()
	if len(gate.entries) != 0 {
		t.Fatalf("gate entries leaked: %v", gate.entries)
	}
	target := &fakeCommandTarget{}
	if _, err := executeConsoleCommand(ctx, target, "test", time.Second); err == nil || target.dispatched != 0 {
		t.Fatalf("cancelled command was submitted: err=%v dispatched=%d", err, target.dispatched)
	}
}
