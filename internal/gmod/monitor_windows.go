//go:build windows && 386

package gmod

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const (
	crashFileGracePeriod  = 3 * time.Second
	unresponsiveThreshold = 10 * time.Second
)

// monitoredProcess holds a real Windows handle so an exit cannot be confused
// with a failed process enumeration or with PID reuse.
type monitoredProcess interface {
	ExitCode() (uint32, bool, error)
	Console() ([]string, error)
	Ready() (bool, error)
	Responsiveness() (responsive, known bool, err error)
	Close() error
}

type gameWatch struct {
	process           monitoredProcess
	gameDir           string
	baseline          crashSnapshot
	event             GameEvent
	exitedAt          time.Time
	exitError         string
	emit              func(GameEvent)
	ready             bool
	unresponsive      bool
	unresponsiveSince time.Time
}

func (w *gameWatch) poll(now time.Time) (GameEvent, bool) {
	if w.exitedAt.IsZero() {
		code, exited, err := w.process.ExitCode()
		if err != nil {
			// An inaccessible process is not evidence of an exit.
			if err.Error() != w.exitError {
				log.Printf("Game watcher PID %d: %v", w.event.PID, err)
				w.exitError = err.Error()
			}
			return GameEvent{}, false
		}
		w.exitError = ""
		if !exited {
			w.observeHealth(now)
			lines, err := w.process.Console()
			if err != nil {
				w.event.ConsoleError = err.Error()
			} else {
				w.event.Console = consoleTail(lines)
				capturedAt := now.UTC()
				w.event.ConsoleCapturedAt = &capturedAt
				w.event.ConsoleError = ""
			}
			return GameEvent{}, false
		}
		w.exitedAt = now
		w.event.OccurredAt, w.event.ExitCode = now.UTC(), code
		_ = w.process.Close()
	}
	// Give the game's crash handler time to finish writing its diagnostic files.
	if now.Sub(w.exitedAt) < crashFileGracePeriod {
		return GameEvent{}, false
	}
	files, diagnostics := collectCrashFiles(w.gameDir, w.baseline)
	w.event.Type = "game_stopped"
	if w.event.ExitCode != 0 || len(files) > 0 {
		w.event.Type = "game_crashed"
		w.event.CrashFiles = files
	} else {
		// Console snapshots are crash diagnostics, not normal shutdown payloads.
		w.event.Console, w.event.ConsoleError = "", ""
		w.event.ConsoleCapturedAt = nil
	}
	w.event.ReportErrors = append(w.event.ReportErrors, diagnostics...)
	return w.event, true
}

func (w *gameWatch) healthEvent(kind string, now time.Time) {
	if w.emit == nil {
		return
	}
	event := GameEvent{Type: kind, PID: w.event.PID, OccurredAt: now.UTC()}
	if kind == "game_unresponsive" || kind == "game_responsive" {
		since := w.unresponsiveSince.UTC()
		event.UnresponsiveSince = &since
		event.UnresponsiveMilliseconds = now.Sub(since).Milliseconds()
	}
	w.emit(event)
}

func (w *gameWatch) observeHealth(now time.Time) {
	responsive, known, err := w.process.Responsiveness()
	if err != nil || !known {
		if !w.unresponsive {
			w.unresponsiveSince = time.Time{}
		}
		return
	}
	if !responsive {
		if w.unresponsiveSince.IsZero() {
			w.unresponsiveSince = now
		}
		if !w.unresponsive && now.Sub(w.unresponsiveSince) >= unresponsiveThreshold {
			w.unresponsive = true
			w.healthEvent("game_unresponsive", now)
		}
		return
	}
	if w.unresponsive {
		w.healthEvent("game_responsive", now)
		w.unresponsive = false
	}
	w.unresponsiveSince = time.Time{}
	if !w.ready {
		if ready, err := w.process.Ready(); err == nil && ready {
			w.ready = true
			w.healthEvent("game_ready", now)
		}
	}
}

type windowsMonitoredProcess struct {
	handle windows.Handle
	pid    uint32
	conn   *GMod
	window uintptr
}

func (p *windowsMonitoredProcess) Responsiveness() (bool, bool, error) {
	if p.window == 0 || windowPID(p.window) != p.pid {
		window, err := gameWindow(p.pid)
		if err != nil {
			return false, false, err
		}
		p.window = window
	}
	return probeWindow(p.window)
}

func (p *windowsMonitoredProcess) Ready() (bool, error) {
	conn, err := connectProcess(p.pid, processAccessMask())
	if err != nil {
		return false, err
	}
	defer conn.Close()
	return conn.EngineInterfaceReady()
}

func (p *windowsMonitoredProcess) ExitCode() (uint32, bool, error) {
	state, err := windows.WaitForSingleObject(p.handle, 0)
	if err != nil {
		return 0, false, err
	}
	if state == uint32(windows.WAIT_TIMEOUT) {
		return 0, false, nil
	}
	if state != windows.WAIT_OBJECT_0 {
		return 0, false, fmt.Errorf("unexpected process wait state: %d", state)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(p.handle, &code); err != nil {
		return 0, false, err
	}
	return code, true, nil
}

func (p *windowsMonitoredProcess) Console() ([]string, error) {
	if p.conn == nil {
		// No remote threads or engine calls: watching should not change the game.
		conn, err := connectProcess(p.pid, windows.PROCESS_VM_READ|windows.PROCESS_QUERY_INFORMATION)
		if err != nil {
			return nil, err
		}
		p.conn = conn
	}
	return p.conn.ReadRecentConsoleLines(100)
}

func (p *windowsMonitoredProcess) Close() error {
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
	return windows.CloseHandle(p.handle)
}

func openGameWatch(pid uint32) (*gameWatch, error) {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return nil, err
	}
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	var gameDir string
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err == nil {
		gameDir = filepath.Dir(windows.UTF16ToString(buf[:size]))
		// Some branches place the executable in bin/win32 or bin/win64.
		if strings.EqualFold(filepath.Base(gameDir), "win32") || strings.EqualFold(filepath.Base(gameDir), "win64") {
			if parent := filepath.Dir(gameDir); strings.EqualFold(filepath.Base(parent), "bin") {
				gameDir = filepath.Dir(parent)
			}
		}
	}
	baseline, diagnostics := snapshotCrashFiles(gameDir)
	return &gameWatch{
		process: &windowsMonitoredProcess{handle: handle, pid: pid},
		gameDir: gameDir, baseline: baseline,
		event: GameEvent{PID: pid, ReportErrors: diagnostics},
	}, nil
}

// WatchGame observes the default Garry's Mod client while ctx is alive. Games
// already running are included. Readiness uses the same engine-interface probe
// as Status; responsiveness uses a bounded Windows message round trip.
func WatchGame(ctx context.Context, emit func(GameEvent)) {
	watches := make(map[uint32]*gameWatch)
	defer func() {
		for _, watch := range watches {
			if watch.exitedAt.IsZero() {
				_ = watch.process.Close()
			}
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		now := time.Now()
		for pid, watch := range watches {
			if event, done := watch.poll(now); done {
				emit(event)
				delete(watches, pid)
			}
		}
		pids, err := findAllProcessIDsByName(defaultProcessName)
		if err == nil {
			for _, pid := range pids {
				if _, exists := watches[pid]; exists {
					continue
				}
				// Exclude helpers, but attach as soon as engine.dll loads, before
				// client.dll is ready, to also observe early startup failures.
				if _, err := findModuleInfo(pid, "engine.dll"); err != nil {
					continue
				}
				if watch, err := openGameWatch(pid); err == nil {
					watch.emit = emit
					watches[pid] = watch
					watch.poll(now)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
