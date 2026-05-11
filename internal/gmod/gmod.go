//go:build windows && 386

// Package gmod provides functions to inspect and control a running Garry's Mod
// client process.
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

	clientCmdVtblOff    = 0x1C // IVEngineClient::ClientCmd
	getMaxClientsOff    = 0x54 // IVEngineClient::GetMaxClients
	isInGameVtblOff     = 0x68 // IVEngineClient::IsInGame
	engineInterfaceName = "VEngineClient015"

	maxConsoleWalk       = 8192
	maxConsoleLineLength = 16384
)

type consoleGlobals struct {
	nodesPtrAddr  uintptr
	chainWordAddr uintptr
}

// GMod represents a connection to a running Garry's Mod process.
type GMod struct {
	handle         windows.Handle
	pid            uint32
	clientBase     uintptr
	clientSize     uint32
	engineBase     uintptr
	engineSize     uint32
	engineIface    uint32
	consoleGlobals consoleGlobals
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

		clientInfo, clientErr := findModuleInfo(pid, "client.dll")
		if clientErr == nil {
			process.Client.Loaded = true
			process.Client.Base = clientInfo.Base
		}

		engineInfo, engineErr := findModuleInfo(pid, "engine.dll")
		if engineErr == nil {
			process.Engine.Loaded = true
			process.Engine.Base = engineInfo.Base
		}

		if !process.Client.Loaded && !process.Engine.Loaded {
			continue
		}

		if process.Client.Loaded && process.Engine.Loaded {
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

	clientInfo, err := findModuleInfo(resolvedPID, "client.dll")
	if err != nil {
		return nil, fmt.Errorf("find client.dll: %w", err)
	}

	engineInfo, err := findModuleInfo(resolvedPID, "engine.dll")
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
		clientBase: clientInfo.Base,
		clientSize: clientInfo.Size,
		engineBase: engineInfo.Base,
		engineSize: engineInfo.Size,
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

// EngineInterfaceReady reports whether IVEngineClient015 is available.
func (g *GMod) EngineInterfaceReady() (bool, error) {
	ifacePtr, err := g.resolveEngineInterface()
	if err != nil {
		return false, fmt.Errorf("resolve engine interface: %w", err)
	}
	return ifacePtr != 0, nil
}

// IsInGame calls IVEngineClient::IsInGame in the target process.
func (g *GMod) IsInGame() (bool, error) {
	ifacePtr, err := g.resolveEngineInterface()
	if err != nil {
		return false, fmt.Errorf("resolve engine interface: %w", err)
	}
	if ifacePtr == 0 {
		return false, fmt.Errorf("%s is NULL", engineInterfaceName)
	}

	method, err := g.resolveEngineMethod(ifacePtr, isInGameVtblOff)
	if err != nil {
		return false, fmt.Errorf("resolve IsInGame: %w", err)
	}

	result, err := g.callThiscallNoArg(method, ifacePtr)
	if err != nil {
		return false, fmt.Errorf("call IsInGame: %w", err)
	}
	return result != 0, nil
}

// MaxPlayers calls IVEngineClient::GetMaxClients in the target process.
func (g *GMod) MaxPlayers() (int, error) {
	ifacePtr, err := g.resolveEngineInterface()
	if err != nil {
		return 0, fmt.Errorf("resolve engine interface: %w", err)
	}
	if ifacePtr == 0 {
		return 0, nil
	}

	method, err := g.resolveEngineMethod(ifacePtr, getMaxClientsOff)
	if err != nil {
		return 0, fmt.Errorf("resolve GetMaxClients: %w", err)
	}

	result, err := g.callThiscallNoArg(method, ifacePtr)
	if err != nil {
		return 0, fmt.Errorf("call GetMaxClients: %w", err)
	}
	return int(result), nil
}

// ReadConsoleLines walks the engine.dll console history linked list in remote memory.
// The engine chain is newest-first on the Garry's Mod build this package targets.
func (g *GMod) ReadConsoleLines() ([]string, error) {
	globals, err := g.resolveConsoleGlobals()
	if err != nil {
		return nil, err
	}

	nodesTable, err := readRemoteUint32(g.handle, globals.nodesPtrAddr)
	if err != nil {
		return nil, fmt.Errorf("read console nodes pointer: %w", err)
	}
	if nodesTable == 0 {
		return nil, fmt.Errorf("console node table is NULL (game still loading or signatures outdated)")
	}

	chainWord, err := readRemoteUint32(g.handle, globals.chainWordAddr)
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
	globals, err := g.resolveConsoleGlobals()
	if err != nil {
		return 0, 0, err
	}
	nodesTablePtr, err = readRemoteUint32(g.handle, globals.nodesPtrAddr)
	if err != nil {
		return 0, 0, err
	}
	chainWord, err = readRemoteUint32(g.handle, globals.chainWordAddr)
	return nodesTablePtr, chainWord, err
}

// RunConsoleCommand executes a console command in the Garry's Mod process.
func (g *GMod) RunConsoleCommand(cmd string) error {
	if strings.TrimSpace(cmd) == "" {
		return fmt.Errorf("command cannot be empty")
	}

	ifacePtr, err := g.resolveEngineInterface()
	if err != nil {
		return fmt.Errorf("resolve engine interface: %w", err)
	}
	if ifacePtr == 0 {
		return fmt.Errorf("%s is NULL - game not fully initialized", engineInterfaceName)
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

func (g *GMod) resolveEngineInterface() (uint32, error) {
	if g.engineIface != 0 {
		return g.engineIface, nil
	}

	createInterface, err := findRemoteExport(g.handle, g.engineBase, "CreateInterface")
	if err != nil {
		return 0, fmt.Errorf("find engine.dll CreateInterface: %w", err)
	}

	remoteName, err := allocRemote(g.handle, uintptr(len(engineInterfaceName)+1), pageReadwrite)
	if err != nil {
		return 0, fmt.Errorf("allocate interface name: %w", err)
	}
	defer freeRemote(g.handle, remoteName)

	if err := writeRemote(g.handle, remoteName, append([]byte(engineInterfaceName), 0)); err != nil {
		return 0, fmt.Errorf("write interface name: %w", err)
	}

	stub := buildCreateInterfaceStub(createInterface)
	remoteStub, err := allocRemote(g.handle, uintptr(len(stub)), pageExecuteReadwrite)
	if err != nil {
		return 0, fmt.Errorf("allocate CreateInterface stub: %w", err)
	}
	defer freeRemote(g.handle, remoteStub)

	if err := writeRemote(g.handle, remoteStub, stub); err != nil {
		return 0, fmt.Errorf("write CreateInterface stub: %w", err)
	}

	result, err := remoteCall(g.handle, remoteStub, remoteName)
	if err != nil {
		return 0, fmt.Errorf("call CreateInterface: %w", err)
	}
	g.engineIface = uint32(result)
	return g.engineIface, nil
}

func (g *GMod) resolveConsoleGlobals() (consoleGlobals, error) {
	if g.consoleGlobals.nodesPtrAddr != 0 && g.consoleGlobals.chainWordAddr != 0 {
		return g.consoleGlobals, nil
	}
	globals, err := findConsoleGlobals(g.handle, g.engineBase, g.engineSize)
	if err != nil {
		return consoleGlobals{}, err
	}
	g.consoleGlobals = globals
	return globals, nil
}

func (g *GMod) callThiscallNoArg(method uint32, thisPtr uint32) (uintptr, error) {
	stub := buildThiscallNoArgStub(uintptr(method), uintptr(thisPtr))
	remoteStub, err := allocRemote(g.handle, uintptr(len(stub)), pageExecuteReadwrite)
	if err != nil {
		return 0, fmt.Errorf("allocate stub: %w", err)
	}
	defer freeRemote(g.handle, remoteStub)

	if err := writeRemote(g.handle, remoteStub, stub); err != nil {
		return 0, fmt.Errorf("write stub: %w", err)
	}
	return remoteCall(g.handle, remoteStub, 0)
}

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
		if _, err := findModuleInfo(p, "client.dll"); err == nil {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no %s process loads client.dll", exe)
}
