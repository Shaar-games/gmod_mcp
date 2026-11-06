package gmod

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestEventHistoryRetentionAndConcurrentReaders(t *testing.T) {
	var history EventHistory
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for range 100 {
			history.Since(0)
		}
	}()
	for range EventHistoryLimit + 3 {
		history.Add(GameEvent{Type: "game_stopped"})
	}
	readers.Wait()
	events, lastID := history.Since(0)
	if len(events) != EventHistoryLimit || lastID != EventHistoryLimit+3 || events[0].ID != 4 {
		t.Fatalf("unexpected retained history: %d events, last ID %d, first ID %d", len(events), lastID, events[0].ID)
	}
	events, _ = history.Since(lastID - 1)
	if len(events) != 1 || events[0].ID != lastID {
		t.Fatalf("cursor returned %v", events)
	}
	events, _ = history.Since(lastID)
	if events == nil || len(events) != 0 {
		t.Fatalf("expected an empty JSON array, got %v", events)
	}
}

func TestCrashReportsIgnoreOldFilesAndBoundText(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "crashes"), 0700); err != nil {
		t.Fatal(err)
	}
	oldDump := filepath.Join(dir, "old.mdmp")
	writeTestFile(t, oldDump, "old dump")
	baseline, diagnostics := snapshotCrashFiles(dir)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	dump := filepath.Join(dir, "new.mdmp")
	writeTestFile(t, dump, "binary dump must never be inlined")
	logPath := filepath.Join(dir, "crashes", "report.log")
	writeTestFile(t, logPath, strings.Repeat("a", maxReportBytes)+"fatal error")
	writeTestFile(t, filepath.Join(dir, "unrelated.txt"), "not a crash report")
	files, diagnostics := collectCrashFiles(dir, baseline)
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0], "not a Windows minidump") || len(files) != 2 {
		t.Fatalf("files=%d diagnostics=%v", len(files), diagnostics)
	}
	for _, file := range files {
		switch file.Path {
		case dump:
			if file.Text != "" || file.Size == 0 {
				t.Fatalf("dump should only contain metadata: %+v", file)
			}
		case logPath:
			if !file.Truncated || len(file.Text) != maxReportBytes || !strings.HasSuffix(file.Text, "fatal error") {
				t.Fatalf("invalid report tail: length=%d truncated=%t", len(file.Text), file.Truncated)
			}
		default:
			t.Fatalf("unexpected file %s", file.Path)
		}
	}
	// Replacing or growing an existing report is also a change.
	writeTestFile(t, oldDump, "updated dump")
	files, _ = collectCrashFiles(dir, baseline)
	if len(files) != 3 {
		t.Fatalf("modified report was missed: %v", files)
	}
}

func TestCrashReportsLimitFilesAndAggregateBytes(t *testing.T) {
	dir := t.TempDir()
	for i := range maxReportFiles + 2 {
		path := filepath.Join(dir, time.Unix(int64(i), 0).Format("150405")+"_crash.log")
		writeTestFile(t, path, strings.Repeat("x", maxReportBytes/2))
	}
	files, diagnostics := collectCrashFiles(dir, nil)
	if len(files) != maxReportFiles || len(diagnostics) != 1 {
		t.Fatalf("got %d files and diagnostics %v", len(files), diagnostics)
	}
	bytes := 0
	for _, file := range files {
		bytes += len(file.Text)
	}
	if bytes > maxReportBytes {
		t.Fatalf("report exceeded aggregate byte budget: %d", bytes)
	}
}

func TestConsoleTail(t *testing.T) {
	text := consoleTail([]string{strings.Repeat("x", maxReportBytes), "last line"})
	if len(text) != maxReportBytes || !strings.HasSuffix(text, "\nlast line") {
		t.Fatalf("invalid console tail: %d bytes", len(text))
	}
}
