package gmod

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	defaultCommandTimeout = 5 * time.Second
	commandSettleTime     = 500 * time.Millisecond
	commandPollInterval   = 50 * time.Millisecond
	commandCaptureLines   = 1000
)

type CommandResult struct {
	Status             string   `json:"status" jsonschema:"executed when the engine processed the command without a recognized console error, error when an error was detected, unconfirmed when execution could not be verified"`
	ExecutionConfirmed bool     `json:"executionConfirmed" jsonschema:"Whether the end marker was observed in the engine console"`
	Output             string   `json:"output" jsonschema:"Console output observed during command execution; unrelated game output may be included"`
	Errors             []string `json:"errors" jsonschema:"Recognized engine or Lua errors observed in the captured output"`
	TimedOut           bool     `json:"timedOut" jsonschema:"Whether the confirmation deadline expired"`
	OutputTruncated    bool     `json:"outputTruncated" jsonschema:"Whether console history or the output byte limit omitted output"`
	CaptureError       string   `json:"captureError,omitempty" jsonschema:"Why console confirmation or capture could not be completed"`
}

type commandTarget interface {
	PID() uint32
	RunConsoleCommand(string) error
	ReadRecentConsoleLines(int) ([]string, error)
}

// processGate serializes operations per PID, with cancellable waiting and no
// permanent map entries for processes that are no longer used.
type processGate struct {
	mu      sync.Mutex
	entries map[uint32]*gateEntry
}

type gateEntry struct {
	token chan struct{}
	refs  int
}

func (g *processGate) acquire(ctx context.Context, pid uint32) (func(), error) {
	g.mu.Lock()
	if g.entries == nil {
		g.entries = make(map[uint32]*gateEntry)
	}
	entry := g.entries[pid]
	if entry == nil {
		entry = &gateEntry{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		g.entries[pid] = entry
	}
	entry.refs++
	g.mu.Unlock()
	dropRef := func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(g.entries, pid)
		}
	}
	select {
	case <-ctx.Done():
		dropRef()
		return nil, ctx.Err()
	case <-entry.token:
		return func() { entry.token <- struct{}{}; dropRef() }, nil
	}
}

var commandGate processGate

// executeConsoleCommand fences a command with unique engine-side echo markers.
// ClientCmd returning only proves submission; seeing the end marker proves the
// command buffer reached the end of this invocation. Semantic error detection is
// necessarily limited to errors the engine/addons print in recognizable forms.
func executeConsoleCommand(ctx context.Context, target commandTarget, command string, timeout time.Duration) (CommandResult, error) {
	result := CommandResult{Status: "unconfirmed", Errors: []string{}}
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsRune(command, 0) {
		return result, fmt.Errorf("command must be nonempty and contain no NUL bytes")
	}
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	release, err := commandGate.acquire(ctx, target.PID())
	if err != nil {
		result.TimedOut = ctx.Err() == context.DeadlineExceeded
		return result, err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return result, fmt.Errorf("create command markers: %w", err)
	}
	token := hex.EncodeToString(random[:])
	begin, end := "__gmod_mcp_begin_"+token, "__gmod_mcp_end_"+token
	if err := target.RunConsoleCommand("echo " + begin + "\n" + command + "\necho " + end + "\n"); err != nil {
		return result, err
	}
	ticker := time.NewTicker(commandPollInterval)
	defer ticker.Stop()
	var confirmedAt time.Time
	for {
		lines, err := target.ReadRecentConsoleLines(commandCaptureLines)
		if err != nil {
			result.CaptureError = err.Error()
		} else {
			frame, started, ended := commandFrame(lines, begin, end)
			if started || ended {
				result.Output = consoleTail(frame)
				result.OutputTruncated = !started || len(strings.Join(frame, "\n")) > maxReportBytes
				result.Errors = commandErrors(frame)
				result.ExecutionConfirmed = ended
				result.CaptureError = ""
				if ended && !started {
					result.CaptureError = "Command start marker was evicted from console history; some output and errors may be missing"
				}
				if ended && confirmedAt.IsZero() {
					confirmedAt = time.Now()
				}
			}
		}
		if !confirmedAt.IsZero() && time.Since(confirmedAt) >= commandSettleTime {
			return finishCommandResult(result), nil
		}
		select {
		case <-ctx.Done():
			result.TimedOut = ctx.Err() == context.DeadlineExceeded
			if !result.ExecutionConfirmed && result.CaptureError == "" {
				result.CaptureError = "The engine did not print the command end marker before capture stopped"
			}
			return finishCommandResult(result), nil
		case <-ticker.C:
		}
	}
}

func finishCommandResult(result CommandResult) CommandResult {
	if len(result.Errors) > 0 {
		result.Status = "error"
	} else if result.ExecutionConfirmed && result.CaptureError == "" {
		result.Status = "executed"
	}
	return result
}

// Console entries may contain several text lines. Markers are compared against
// complete lines so a command echo or unrelated occurrence cannot confirm it.
func commandFrame(entries []string, begin, end string) ([]string, bool, bool) {
	lines := strings.Split(strings.Join(entries, "\n"), "\n")
	for i := range lines {
		lines[i] = consoleMessage(lines[i])
	}
	start, stop := -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case begin:
			start = i
		case end:
			stop = i
		}
	}
	if start < 0 && stop < 0 {
		return nil, false, false
	}
	if stop >= 0 && start > stop {
		return nil, false, false
	}
	// Include output arriving during the short settling interval after the end
	// marker, e.g. asynchronous server replies. This is temporal correlation.
	output := make([]string, 0, len(lines))
	for i := start + 1; i < len(lines); i++ {
		if i != stop && strings.TrimSpace(lines[i]) != "" {
			output = append(output, lines[i])
		}
	}
	return output, start >= 0, stop >= 0
}

func commandErrors(lines []string) []string {
	errors := []string{}
	remaining := maxReportBytes
	for _, line := range lines {
		text := strings.ToLower(consoleMessage(line))
		if strings.HasPrefix(text, "unknown command") || strings.HasPrefix(text, "[error]") ||
			strings.HasPrefix(text, "error:") || strings.HasPrefix(text, "lua error:") ||
			strings.HasPrefix(text, "usage:") || strings.HasPrefix(text, "can't use cheat") ||
			strings.HasPrefix(text, "cannot use cheat") || strings.HasPrefix(text, "can't change replicated") ||
			strings.HasPrefix(text, "can't set ") || strings.HasPrefix(text, "cannot set ") ||
			strings.Contains(text, "command is not allowed") || strings.Contains(text, "command blocked") {
			message := consoleTail([]string{line})
			if len(message) > remaining {
				message = strings.ToValidUTF8(message[:remaining], "?")
			}
			errors = append(errors, message)
			remaining -= len(message)
			if remaining == 0 {
				break
			}
		}
	}
	return errors
}
