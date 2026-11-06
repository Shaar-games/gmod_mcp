//go:build windows && 386

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gmod_mcp/internal/gmod"
)

type fakeExperimentTarget struct{ history *gmod.EventHistory }

func (*fakeExperimentTarget) PID() uint32 { return 1234 }
func (*fakeExperimentTarget) BinaryFingerprints() []gmod.FileFingerprint {
	return []gmod.FileFingerprint{{Path: "client.dll", SHA256: "test-hash"}}
}
func (f *fakeExperimentTarget) ExecuteConsoleCommand(_ context.Context, _ string, _ time.Duration) (gmod.CommandResult, error) {
	f.history.Add(gmod.GameEvent{PID: 9999, Type: "game_stopped"})
	f.history.Add(gmod.GameEvent{PID: f.PID(), Type: "game_crashed", ExitCode: 1})
	return gmod.CommandResult{Status: "unconfirmed", TimedOut: true}, nil
}

func TestExperimentRecordsCrashDespiteUnconfirmedCommand(t *testing.T) {
	var history gmod.EventHistory
	history.Add(gmod.GameEvent{PID: 1234, Type: "game_stopped"})
	asset := filepath.Join(t.TempDir(), "probe.vtf")
	if err := os.WriteFile(asset, []byte("test asset"), 0600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	save := func(result RunExperimentOutput) (string, error) { return saveExperimentAt(root, result) }
	result := recordExperimentWithSave(context.Background(), &fakeExperimentTarget{&history}, &history,
		RunExperimentInput{Command: "test_command", ObserveSeconds: 1, FilePaths: []string{asset}}, save)
	if !result.Success || result.Outcome != "game_crashed" || result.Event == nil || result.Event.PID != 1234 ||
		result.CommandResult.Status != "unconfirmed" || result.EvidencePath == "" || len(result.Files) != 1 || result.Files[0].SHA256 == "" {
		t.Fatalf("incomplete experiment record: %+v", result)
	}
	data, err := os.ReadFile(result.EvidencePath)
	if err != nil {
		t.Fatal(err)
	}
	var saved RunExperimentOutput
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Event == nil || saved.Event.Type != "game_crashed" || saved.EvidencePath != result.EvidencePath {
		t.Fatalf("incorrect saved evidence: %+v", saved)
	}
}
