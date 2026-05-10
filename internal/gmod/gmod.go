//go:build windows && 386

// Package gmod provides functions to execute console commands in a running
// Garry's Mod (Source Engine) process by resolving IVEngineClient015 from
// client.dll and invoking ClientCmd via remote thread injection.
package gmod

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const (
	defaultProcessName = "gmod.exe"
	garrysModSteamID   = 4000

	engineClientRVA  = 0x7BD390 // dword_107BD390 in client.dll — stores CreateInterface("VEngineClient015") result
	clientCmdVtblOff = 28       // IVEngineClient vtable[7] = ClientCmd offset
	isInGameVtblOff  = 0x68     // IVEngineClient::IsInGame
	gameRulesPtrRVA  = 0x717050 // off_10717050; +0x1C stores max players on the analyzed client.dll build.

	// engine.dll - console history linked list (matches sub_101F38C0 / crash dump path in the
	// Garry's Mod engine.dll build analyzed with IDA).
	// IDA image base: 0x10000000; globals: 0x1042F070 / 0x1042F07C.
	// These offsets can change when Garry's Mod ships a different engine.dll.
	engineConsoleNodesRVA = 0x42F070 // dword_1042F070 - pointer to node array (each node 8 bytes)
	engineConsoleChainRVA = 0x42F07C // dword_1042F07C - console chain word; head index = HIWORD

	maxConsoleWalk       = 8192
	maxConsoleLineLength = 16384
)

// GMod represents a connection to a running Garry's Mod process.
type GMod struct {
	handle     windows.Handle
	pid        uint32
	clientBase uintptr
	engineBase uintptr
}

type ModuleStatus struct {
	Loaded bool    `json:"loaded"`
	Base   uintptr `json:"base,omitempty"`
	Error  string  `json:"error,omitempty"`
}

type ProcessStatus struct {
	PID         uint32       `json:"pid"`
	Client      ModuleStatus `json:"client"`
	Engine      ModuleStatus `json:"engine"`
	Ready       bool         `json:"ready"`
	EngineAPI   bool         `json:"engineApi"`
	InGame      bool         `json:"inGame"`
	SessionType string       `json:"sessionType"`
	MaxPlayers  int          `json:"maxPlayers,omitempty"`
	Error       string       `json:"error,omitempty"`
}

type GameStatus struct {
	ProcessName string          `json:"processName"`
	Running     bool            `json:"running"`
	Ready       bool            `json:"ready"`
	InGame      bool            `json:"inGame"`
	SessionType string          `json:"sessionType"`
	Processes   []ProcessStatus `json:"processes"`
}

type WaitResult struct {
	Status   GameStatus
	Ready    bool
	TimedOut bool
}

func normalizeProcessName(name string) string {
	exe := strings.TrimSpace(name)
	if exe == "" {
		return defaultProcessName
	}
	return exe
}

func classifySession(inGame bool, maxPlayers int) string {
	if !inGame {
		return "menu"
	}
	if maxPlayers == 1 {
		return "local"
	}
	if maxPlayers > 1 {
		return "server"
	}
	return "unknown"
}

// Status returns whether Garry's Mod is running, loaded enough for tools, and currently in a map.
func Status(processName string) (GameStatus, error) {
	exe := normalizeProcessName(processName)
	status := GameStatus{ProcessName: exe, SessionType: "not_running"}

	pids, err := findAllProcessIDsByName(exe)
	if err != nil {
		return status, nil
	}
	status.Running = len(pids) > 0
	status.SessionType = "menu"

	for _, pid := range pids {
		process := ProcessStatus{PID: pid, SessionType: "loading"}

		clientBase, clientErr := findModuleBase(pid, "client.dll")
		if clientErr == nil {
			process.Client.Loaded = true
			process.Client.Base = clientBase
		}

		engineBase, engineErr := findModuleBase(pid, "engine.dll")
		if engineErr == nil {
			process.Engine.Loaded = true
			process.Engine.Base = engineBase
		}

		if !process.Client.Loaded && !process.Engine.Loaded {
			continue
		}

		modulesReady := process.Client.Loaded && process.Engine.Loaded
		if modulesReady {
			conn, err := Connect(pid, exe)
			if err != nil {
				process.Error = err.Error()
			} else {
				engineAPI, engineAPIErr := conn.EngineInterfaceReady()
				if engineAPIErr != nil {
					process.Error = engineAPIErr.Error()
				}
				process.EngineAPI = engineAPI
				process.Ready = engineAPI

				if process.Ready {
					inGame, inGameErr := conn.IsInGame()
					if inGameErr != nil {
						process.Error = inGameErr.Error()
					} else {
						process.InGame = inGame
					}
				}

				maxPlayers, maxPlayersErr := conn.MaxPlayers()
				_ = conn.Close()

				if maxPlayersErr == nil {
					process.MaxPlayers = maxPlayers
				}
				process.SessionType = classifySession(process.InGame, process.MaxPlayers)
			}
		}

		status.Ready = status.Ready || process.Ready
		status.InGame = status.InGame || process.InGame
		if process.InGame {
			status.SessionType = process.SessionType
		}
		status.Processes = append(status.Processes, process)
	}

	return status, nil
}

// WaitForReady polls Status until the main Garry's Mod process is ready or the timeout expires.
func WaitForReady(ctx context.Context, processName string, timeout time.Duration, interval time.Duration) (WaitResult, error) {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	if interval <= 0 {
		interval = time.Second
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		status, err := Status(processName)
		if err != nil {
			return WaitResult{Status: status}, err
		}
		if status.Ready {
			return WaitResult{Status: status, Ready: true}, nil
		}

		select {
		case <-ctx.Done():
			return WaitResult{Status: status}, ctx.Err()
		case <-deadline.C:
			return WaitResult{Status: status, TimedOut: true}, nil
		case <-ticker.C:
		}
	}
}

// LaunchViaSteam asks Steam to launch Garry's Mod using the Steam URI protocol.
func LaunchViaSteam(appID int) error {
	if appID <= 0 {
		appID = garrysModSteamID
	}
	return shellOpen("steam://run/" + strconv.Itoa(appID))
}

// Connect finds and opens a handle to the Garry's Mod process.
// If pid is 0, it auto-discovers the process by looking for a process named
// processName (defaults to "gmod.exe") that has client.dll loaded.
func Connect(pid uint32, processName string) (*GMod, error) {
	resolvedPID, err := resolvePID(pid, processName)
	if err != nil {
		return nil, fmt.Errorf("resolve PID: %w", err)
	}

	clientBase, err := findModuleBase(resolvedPID, "client.dll")
	if err != nil {
		return nil, fmt.Errorf("find client.dll: %w", err)
	}

	engineBase, err := findModuleBase(resolvedPID, "engine.dll")
	if err != nil {
		return nil, fmt.Errorf("find engine.dll: %w", err)
	}

	h, err := openTargetProcess(resolvedPID)
	if err != nil {
		return nil, fmt.Errorf("open process: %w", err)
	}

	return &GMod{
		handle:     h,
		pid:        resolvedPID,
		clientBase: clientBase,
		engineBase: engineBase,
	}, nil
}

// Close releases the process handle.
func (g *GMod) Close() error {
	if g.handle != windows.InvalidHandle && g.handle != 0 {
		return windows.CloseHandle(g.handle)
	}
	return nil
}

// PID returns the process ID of the connected Garry's Mod instance.
func (g *GMod) PID() uint32 {
	return g.pid
}

// ClientBase returns the base address of client.dll in the target process.
func (g *GMod) ClientBase() uintptr {
	return g.clientBase
}

// EngineBase returns the base address of engine.dll in the target process.
func (g *GMod) EngineBase() uintptr {
	return g.engineBase
}

// EngineInterfaceReady reports whether IVEngineClient015 is available in client.dll.
func (g *GMod) EngineInterfaceReady() (bool, error) {
	ifacePtr, err := g.readEngineInterface()
	if err != nil {
		return false, fmt.Errorf("read engine interface: %w", err)
	}
	return ifacePtr != 0, nil
}

// IsInGame calls IVEngineClient::IsInGame in the target process.
func (g *GMod) IsInGame() (bool, error) {
	ifacePtr, err := g.readEngineInterface()
	if err != nil {
		return false, fmt.Errorf("read engine interface: %w", err)
	}
	if ifacePtr == 0 {
		return false, fmt.Errorf("IVEngineClient015 is NULL")
	}

	method, err := g.resolveEngineMethod(ifacePtr, isInGameVtblOff)
	if err != nil {
		return false, fmt.Errorf("resolve IsInGame: %w", err)
	}

	stub := buildThiscallNoArgStub(uintptr(method), uintptr(ifacePtr))
	remoteStub, err := allocRemote(g.handle, uintptr(len(stub)), pageExecuteReadwrite)
	if err != nil {
		return false, fmt.Errorf("allocate IsInGame stub: %w", err)
	}
	defer freeRemote(g.handle, remoteStub)

	if err := writeRemote(g.handle, remoteStub, stub); err != nil {
		return false, fmt.Errorf("write IsInGame stub: %w", err)
	}

	result, err := remoteCall(g.handle, remoteStub, 0)
	if err != nil {
		return false, fmt.Errorf("call IsInGame: %w", err)
	}
	return result != 0, nil
}

// MaxPlayers reads the clientside game rules max player count used by the Lua game.MaxPlayers binding.
func (g *GMod) MaxPlayers() (int, error) {
	gameRulesPtr, err := readRemoteUint32(g.handle, g.clientBase+uintptr(gameRulesPtrRVA))
	if err != nil {
		return 0, fmt.Errorf("read game rules pointer: %w", err)
	}
	if gameRulesPtr == 0 {
		return 0, nil
	}
	maxPlayers, err := readRemoteUint32(g.handle, uintptr(gameRulesPtr)+0x1C)
	if err != nil {
		return 0, fmt.Errorf("read max players: %w", err)
	}
	return int(maxPlayers), nil
}

// ReadConsoleLines walks the engine.dll console history linked list in remote memory.
// The engine chain is newest-first on the Garry's Mod build this package targets.
func (g *GMod) ReadConsoleLines() ([]string, error) {
	nodesPtrAddr := g.engineBase + uintptr(engineConsoleNodesRVA)
	chainWordAddr := g.engineBase + uintptr(engineConsoleChainRVA)

	nodesTable, err := readRemoteUint32(g.handle, nodesPtrAddr)
	if err != nil {
		return nil, fmt.Errorf("read console nodes pointer: %w", err)
	}
	if nodesTable == 0 {
		return nil, fmt.Errorf("console node table is NULL (game still loading or offsets outdated)")
	}

	chainWord, err := readRemoteUint32(g.handle, chainWordAddr)
	if err != nil {
		return nil, fmt.Errorf("read console chain word: %w", err)
	}

	idx := uint16((chainWord >> 16) & 0xffff)
	if idx == 0xffff {
		return []string{}, nil
	}

	var lines []string
	seen := make(map[uint16]struct{}, 256)

	for step := 0; step < maxConsoleWalk; step++ {
		if _, dup := seen[idx]; dup {
			return nil, fmt.Errorf("console chain cycle detected at index %d", idx)
		}
		seen[idx] = struct{}{}

		nodeAddr := uintptr(nodesTable) + uintptr(idx)*8

		strPtr, err := readRemoteUint32(g.handle, nodeAddr)
		if err != nil {
			return nil, fmt.Errorf("read console line pointer: %w", err)
		}

		var line string
		if strPtr != 0 {
			line, err = readRemoteCString(g.handle, uintptr(strPtr), maxConsoleLineLength)
			if err != nil {
				return nil, fmt.Errorf("read console line text: %w", err)
			}
		}
		lines = append(lines, line)

		nextIdx, err := readRemoteUint16(g.handle, nodeAddr+4)
		if err != nil {
			return nil, fmt.Errorf("read console chain next: %w", err)
		}
		if nextIdx == 0xffff {
			break
		}
		idx = nextIdx
	}

	return lines, nil
}

// RecentConsoleLines returns the most recent lines in chronological order.
func RecentConsoleLines(lines []string, maxLines int) []string {
	if maxLines <= 0 || maxLines > len(lines) {
		maxLines = len(lines)
	}
	out := make([]string, maxLines)
	for i := 0; i < maxLines; i++ {
		out[i] = lines[maxLines-1-i]
	}
	return out
}

// EngineConsoleRawGlobals returns the raw engine.dll values used for console history walking (debugging).
func (g *GMod) EngineConsoleRawGlobals() (nodesTablePtr uint32, chainWord uint32, err error) {
	nodesPtrAddr := g.engineBase + uintptr(engineConsoleNodesRVA)
	chainWordAddr := g.engineBase + uintptr(engineConsoleChainRVA)
	nodesTablePtr, err = readRemoteUint32(g.handle, nodesPtrAddr)
	if err != nil {
		return 0, 0, err
	}
	chainWord, err = readRemoteUint32(g.handle, chainWordAddr)
	return nodesTablePtr, chainWord, err
}

// RunConsoleCommand executes a console command in the Garry's Mod process.
// The command is injected via CreateRemoteThread calling ClientCmd on
// the IVEngineClient015 interface.
func (g *GMod) RunConsoleCommand(cmd string) error {
	if strings.TrimSpace(cmd) == "" {
		return fmt.Errorf("command cannot be empty")
	}

	ifacePtr, err := g.readEngineInterface()
	if err != nil {
		return fmt.Errorf("read engine interface: %w", err)
	}
	if ifacePtr == 0 {
		return fmt.Errorf("IVEngineClient015 is NULL — game not fully initialized")
	}

	clientCmdAddr, err := g.resolveClientCmd(ifacePtr)
	if err != nil {
		return fmt.Errorf("resolve ClientCmd: %w", err)
	}

	remoteCmd, err := allocRemote(g.handle, uintptr(len(cmd)+1), pageReadwrite)
	if err != nil {
		return fmt.Errorf("allocate command buffer: %w", err)
	}
	defer freeRemote(g.handle, remoteCmd)

	if err := writeRemote(g.handle, remoteCmd, append([]byte(cmd), 0)); err != nil {
		return fmt.Errorf("write command: %w", err)
	}

	stub := buildClientCmdStub(uintptr(clientCmdAddr), uintptr(ifacePtr))
	remoteStub, err := allocRemote(g.handle, uintptr(len(stub)), pageExecuteReadwrite)
	if err != nil {
		return fmt.Errorf("allocate stub: %w", err)
	}
	defer freeRemote(g.handle, remoteStub)

	if err := writeRemote(g.handle, remoteStub, stub); err != nil {
		return fmt.Errorf("write stub: %w", err)
	}

	if _, err := remoteCall(g.handle, remoteStub, remoteCmd); err != nil {
		return fmt.Errorf("execute command: %w", err)
	}

	return nil
}

// readEngineInterface reads the IVEngineClient015 interface pointer from client.dll.
func (g *GMod) readEngineInterface() (uint32, error) {
	return readRemoteUint32(g.handle, g.clientBase+uintptr(engineClientRVA))
}

// resolveClientCmd reads the vtable and resolves the ClientCmd function pointer.
func (g *GMod) resolveClientCmd(ifacePtr uint32) (uint32, error) {
	return g.resolveEngineMethod(ifacePtr, clientCmdVtblOff)
}

func (g *GMod) resolveEngineMethod(ifacePtr uint32, vtblOffset uintptr) (uint32, error) {
	vtable, err := readRemoteUint32(g.handle, uintptr(ifacePtr))
	if err != nil {
		return 0, fmt.Errorf("read vtable: %w", err)
	}
	method, err := readRemoteUint32(g.handle, uintptr(vtable)+vtblOffset)
	if err != nil {
		return 0, fmt.Errorf("read method from vtable: %w", err)
	}
	return method, nil
}

// resolvePID finds the PID of the Garry's Mod process.
func resolvePID(pid uint32, name string) (uint32, error) {
	if pid != 0 {
		return pid, nil
	}
	exe := normalizeProcessName(name)
	pids, err := findAllProcessIDsByName(exe)
	if err != nil {
		return 0, err
	}
	for _, p := range pids {
		if _, err := findModuleBase(p, "client.dll"); err == nil {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no %s process loads client.dll", exe)
}
