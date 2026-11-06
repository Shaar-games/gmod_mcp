//go:build windows && 386

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gmod_mcp/internal/gmod"
)

type RunExperimentInput struct {
	Command        string   `json:"command" jsonschema:"Console command or Lua probe to execute"`
	ObserveSeconds int      `json:"observeSeconds,omitempty" jsonschema:"Seconds to watch for a game stop or crash after command capture (default: 5, max: 60)"`
	FilePaths      []string `json:"filePaths,omitempty" jsonschema:"Up to eight local test assets to fingerprint before running the command"`
	processTarget
}

type RunExperimentOutput struct {
	Success        bool                   `json:"success"`
	Message        string                 `json:"message"`
	PID            uint32                 `json:"pid"`
	StartedAt      time.Time              `json:"startedAt"`
	CompletedAt    time.Time              `json:"completedAt"`
	Command        string                 `json:"command"`
	CommandResult  gmod.CommandResult     `json:"commandResult"`
	CommandMessage string                 `json:"commandMessage"`
	Binaries       []gmod.FileFingerprint `json:"binaries"`
	Files          []gmod.FileFingerprint `json:"files"`
	Outcome        string                 `json:"outcome" jsonschema:"game_crashed, game_stopped, or no_exit_observed"`
	Event          *gmod.GameEvent        `json:"event,omitempty"`
	EvidencePath   string                 `json:"evidencePath,omitempty"`
	EvidenceError  string                 `json:"evidenceError,omitempty"`
}

type experimentTarget interface {
	consoleCommander
	BinaryFingerprints() []gmod.FileFingerprint
}

func recordExperiment(ctx context.Context, target experimentTarget, history *gmod.EventHistory, input RunExperimentInput) RunExperimentOutput {
	return recordExperimentWithSave(ctx, target, history, input, saveExperiment)
}

func recordExperimentWithSave(ctx context.Context, target experimentTarget, history *gmod.EventHistory, input RunExperimentInput, save func(RunExperimentOutput) (string, error)) RunExperimentOutput {
	started := time.Now().UTC()
	result := RunExperimentOutput{
		PID: target.PID(), StartedAt: started, Command: input.Command,
		Binaries: target.BinaryFingerprints(), Files: []gmod.FileFingerprint{}, Outcome: "no_exit_observed",
	}
	for _, path := range input.FilePaths {
		result.Files = append(result.Files, gmod.FingerprintFile(path))
	}
	_, cursor := history.Since(0)
	command := runConfirmedConsoleCommand(ctx, target, input.Command, 5*time.Second)
	result.CommandResult, result.CommandMessage = command.CommandResult, command.Message
	return observeExperiment(ctx, target, history, cursor, input.ObserveSeconds, result, save)
}

func observeExperiment(ctx context.Context, target experimentTarget, history *gmod.EventHistory, cursor uint64, seconds int, result RunExperimentOutput, save func(RunExperimentOutput) (string, error)) RunExperimentOutput {
	if seconds <= 0 {
		seconds = 5
	}
	if seconds > 60 {
		seconds = 60
	}
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		events, _ := history.Since(cursor)
		for _, event := range events {
			if event.PID == target.PID() && (event.Type == "game_crashed" || event.Type == "game_stopped") {
				result.Outcome, result.Event = event.Type, &event
				return finishExperiment(result, save)
			}
		}
		select {
		case <-ctx.Done():
			result.Message = fmt.Sprintf("Observation interrupted: %v", ctx.Err())
			return finishExperiment(result, save)
		case <-timer.C:
			return finishExperiment(result, save)
		case <-ticker.C:
		}
	}
}

func finishExperiment(result RunExperimentOutput, save func(RunExperimentOutput) (string, error)) RunExperimentOutput {
	result.CompletedAt = time.Now().UTC()
	if result.Message == "" {
		result.Message = "Experiment evidence recorded; inspect commandResult and outcome"
	}
	result.Success = true
	path, err := save(result)
	if err != nil {
		result.Success = false
		result.EvidenceError = err.Error()
	} else {
		result.EvidencePath = path
	}
	return result
}

func saveExperiment(result RunExperimentOutput) (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return saveExperimentAt(filepath.Join(root, "gmod-mcp", "experiments"), result)
}

func saveExperimentAt(directory string, result RunExperimentOutput) (string, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-pid%d.json", result.StartedAt.Format("20060102T150405.000000000Z"), result.PID)
	path := filepath.Join(directory, name)
	result.EvidencePath = path
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func registerExperiment(server *mcp.Server, history *gmod.EventHistory) {
	mcp.AddTool(server, &mcp.Tool{Name: "RunExperiment", Description: "Run one console probe, fingerprint the game binaries and selected test files, observe a stop/crash, and save a structured evidence record in the user's cache directory."},
		func(ctx context.Context, _ *mcp.CallToolRequest, input RunExperimentInput) (*mcp.CallToolResult, RunExperimentOutput, error) {
			input.Command = strings.TrimSpace(input.Command)
			if input.Command == "" || strings.ContainsRune(input.Command, 0) || len(input.FilePaths) > 8 {
				return &mcp.CallToolResult{IsError: true}, RunExperimentOutput{Message: "Command must be nonempty, without NUL; at most eight file paths are accepted"}, nil
			}
			conn, err := connectToGMod(input.processTarget)
			if err != nil {
				return &mcp.CallToolResult{IsError: true}, RunExperimentOutput{Message: err.Error()}, nil
			}
			defer conn.Close()
			result := recordExperiment(ctx, conn, history, input)
			return &mcp.CallToolResult{IsError: result.EvidenceError != ""}, result, nil
		})
}
