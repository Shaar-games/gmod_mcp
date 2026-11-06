package gmod

import (
	"fmt"
	"gmod_mcp/internal/minidump"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	EventHistoryLimit = 32
	maxReportFiles    = 8
	maxReportBytes    = 64 * 1024
)

// GameEvent describes readiness, responsiveness transitions and process exits.
// ExitCode only applies to exits; nonzero codes may also mean forced termination.
type GameEvent struct {
	ID                       uint64      `json:"id"`
	Type                     string      `json:"type"`
	PID                      uint32      `json:"pid"`
	OccurredAt               time.Time   `json:"occurredAt"`
	ExitCode                 uint32      `json:"exitCode"`
	Console                  string      `json:"console,omitempty"`
	ConsoleCapturedAt        *time.Time  `json:"consoleCapturedAt,omitempty"`
	ConsoleError             string      `json:"consoleError,omitempty"`
	CrashFiles               []CrashFile `json:"crashFiles,omitempty"`
	ReportErrors             []string    `json:"reportErrors,omitempty"`
	UnresponsiveSince        *time.Time  `json:"unresponsiveSince,omitempty"`
	UnresponsiveMilliseconds int64       `json:"unresponsiveMilliseconds,omitempty"`
}

type CrashFile struct {
	Path      string            `json:"path"`
	Size      int64             `json:"size"`
	Text      string            `json:"text,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
	Dump      *minidump.Summary `json:"dump,omitempty"`
}

// EventHistory retains a bounded, chronological history independent of whether
// a client enables logging or subscribes to resource updates.
type EventHistory struct {
	mu     sync.Mutex
	lastID uint64
	events []GameEvent
}

func (h *EventHistory) Add(event GameEvent) GameEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastID++
	event.ID = h.lastID
	if len(h.events) == EventHistoryLimit {
		copy(h.events, h.events[1:])
		h.events = h.events[:EventHistoryLimit-1]
	}
	h.events = append(h.events, event)
	return event
}

func (h *EventHistory) Since(afterID uint64) ([]GameEvent, uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	events := make([]GameEvent, 0, len(h.events))
	for _, event := range h.events {
		if event.ID > afterID {
			events = append(events, event)
		}
	}
	return events, h.lastID
}

type fileStamp struct {
	size    int64
	modTime time.Time
}

type crashSnapshot map[string]fileStamp

// Only known diagnostic locations are scanned, without recursion or reading
// binary dumps. Baselines prevent a previous crash from classifying a clean exit.
func snapshotCrashFiles(gameDir string) (crashSnapshot, []string) {
	files := make(crashSnapshot)
	if gameDir == "" {
		return files, []string{"Game directory unavailable; crash files could not be inspected"}
	}
	var diagnostics []string
	for _, dir := range []string{gameDir, filepath.Join(gameDir, "crashes")} {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("Scan %s: %v", dir, err))
			continue
		}
		for _, entry := range entries {
			name := strings.ToLower(entry.Name())
			ext := filepath.Ext(name)
			isDump := ext == ".mdmp" || ext == ".dmp"
			isLog := (ext == ".log" || ext == ".txt") &&
				(dir != gameDir || strings.Contains(name, "crash"))
			if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || (!isDump && !isLog) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				diagnostics = append(diagnostics, fmt.Sprintf("Inspect %s: %v", path, err))
				continue
			}
			if info.Mode().IsRegular() {
				files[path] = fileStamp{info.Size(), info.ModTime()}
			}
		}
	}
	return files, diagnostics
}

func collectCrashFiles(gameDir string, baseline crashSnapshot) ([]CrashFile, []string) {
	current, diagnostics := snapshotCrashFiles(gameDir)
	var paths []string
	for path, stamp := range current {
		if old, exists := baseline[path]; !exists || old != stamp {
			paths = append(paths, path)
		}
	}
	sort.Slice(paths, func(i, j int) bool {
		a, b := current[paths[i]], current[paths[j]]
		if a.modTime.Equal(b.modTime) {
			return paths[i] < paths[j]
		}
		return a.modTime.After(b.modTime)
	})
	if len(paths) > maxReportFiles {
		diagnostics = append(diagnostics, fmt.Sprintf("Only the newest %d of %d crash files are included", maxReportFiles, len(paths)))
		paths = paths[:maxReportFiles]
	}
	var files []CrashFile
	remaining := maxReportBytes
	for _, path := range paths {
		file := CrashFile{Path: path, Size: current[path].size}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".log" || ext == ".txt" {
			text, truncated, err := readFileTail(path, remaining)
			if err != nil {
				diagnostics = append(diagnostics, fmt.Sprintf("Read %s: %v", path, err))
			} else {
				file.Text, file.Truncated = text, truncated
				remaining -= len(text)
			}
		}
		if ext == ".mdmp" || ext == ".dmp" {
			summary, err := minidump.ParseFile(path)
			if err != nil {
				diagnostics = append(diagnostics, fmt.Sprintf("Parse dump %s: %v", path, err))
			} else {
				file.Dump = &summary
			}
		}
		files = append(files, file)
	}
	return files, diagnostics
}

func readFileTail(path string, limit int) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", false, err
	}
	start := max(int64(0), info.Size()-int64(limit))
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", false, err
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)))
	// Keep byte budgets meaningful even if a log contains invalid UTF-8.
	return strings.ToValidUTF8(string(data), "?"), start > 0, err
}

func consoleTail(lines []string) string {
	text := strings.Join(lines, "\n")
	if len(text) > maxReportBytes {
		text = text[len(text)-maxReportBytes:]
	}
	return strings.ToValidUTF8(text, "?")
}
