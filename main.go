//go:build windows && 386

// gmod_mcp is an MCP (Model Context Protocol) server that exposes tools
// for interacting with a running Garry's Mod process.
//
// It uses the official Go MCP SDK and communicates over stdin/stdout (stdio transport).
//
// Build:
//
//	build.cmd
//
// Usage with Claude Desktop / Cursor:
//
//	Add to your MCP settings:
//	  {
//	    "mcpServers": {
//	      "gmod": {
//	        "command": "path/to/bin/gmod_mcp.exe"
//	      }
//	    }
//	  }
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gmod_mcp/internal/gmod"
)

const (
	serverName    = "gmod-mcp"
	serverVersion = "1.3.0"
)

type processTarget struct {
	PID         int    `json:"pid,omitempty"           jsonschema:"Process ID of the Garry's Mod instance (overrides processName)"`
	ProcessName string `json:"processName,omitempty"   jsonschema:"Process image name to search for (default: gmod.exe)"`
}

func connectToGMod(target processTarget) (*gmod.GMod, error) {
	var pid uint32
	if target.PID > 0 {
		pid = uint32(target.PID)
	}
	return gmod.Connect(pid, target.ProcessName)
}

type RunConsoleCommandInput struct {
	Command string `json:"command"       jsonschema:"The Source Engine console command to execute"`
	processTarget
}

type GetGameStatusInput struct {
	ProcessName string `json:"processName,omitempty" jsonschema:"Process image name to search for (default: gmod.exe)"`
}

type GetGameStatusOutput struct {
	Success bool            `json:"success" jsonschema:"Whether the game status check completed"`
	Message string          `json:"message" jsonschema:"Human-readable status message"`
	Status  gmod.GameStatus `json:"status"  jsonschema:"Garry's Mod process and session status"`
}

func handleGetGameStatus(ctx context.Context, req *mcp.CallToolRequest, input GetGameStatusInput) (
	*mcp.CallToolResult, GetGameStatusOutput, error,
) {
	status, err := gmod.Status(input.ProcessName)
	if err != nil {
		return nil, GetGameStatusOutput{
			Success: false,
			Message: fmt.Sprintf("Failed to check Garry's Mod status: %v", err),
			Status:  status,
		}, nil
	}

	message := "Garry's Mod is not running"
	if status.Running {
		message = "Garry's Mod is running"
	}
	if status.Ready {
		message = fmt.Sprintf("Garry's Mod is ready (session: %s, inGame: %t)", status.SessionType, status.InGame)
	}

	return nil, GetGameStatusOutput{
		Success: true,
		Message: message,
		Status:  status,
	}, nil
}

type LaunchGameInput struct {
	TimeoutSeconds int `json:"timeoutSeconds,omitempty" jsonschema:"Maximum seconds to wait for Garry's Mod to be ready (default: 90, max: 600)"`
}

type LaunchGameOutput struct {
	Success  bool            `json:"success" jsonschema:"Whether Garry's Mod became ready before the timeout"`
	Message  string          `json:"message" jsonschema:"Human-readable result message"`
	AppID    int             `json:"appId"   jsonschema:"Steam app ID used for launch"`
	TimedOut bool            `json:"timedOut" jsonschema:"Whether waiting for the game timed out"`
	Status   gmod.GameStatus `json:"status"   jsonschema:"Final Garry's Mod status observed by the launcher"`
}

func handleLaunchGame(ctx context.Context, req *mcp.CallToolRequest, input LaunchGameInput) (
	*mcp.CallToolResult, LaunchGameOutput, error,
) {
	const appID = 4000
	timeout := input.TimeoutSeconds
	if timeout <= 0 {
		timeout = 90
	}
	if timeout > 600 {
		timeout = 600
	}

	if err := gmod.LaunchViaSteam(appID); err != nil {
		return nil, LaunchGameOutput{
			Success: false,
			Message: fmt.Sprintf("Failed to ask Steam to launch app %d: %v", appID, err),
			AppID:   appID,
		}, nil
	}

	wait, err := gmod.WaitForReady(ctx, "", time.Duration(timeout)*time.Second, time.Second)
	if err != nil {
		return nil, LaunchGameOutput{
			Success: false,
			Message: fmt.Sprintf("Steam launch requested, but Garry's Mod did not become ready: %v", err),
			AppID:   appID,
			Status:  wait.Status,
		}, nil
	}
	if wait.TimedOut {
		return nil, LaunchGameOutput{
			Success:  false,
			Message:  fmt.Sprintf("Steam launch requested, but Garry's Mod was not ready within %d seconds", timeout),
			AppID:    appID,
			TimedOut: true,
			Status:   wait.Status,
		}, nil
	}

	return nil, LaunchGameOutput{
		Success: true,
		Message: fmt.Sprintf(
			"Garry's Mod is ready after Steam launch request (session: %s, inGame: %t)",
			wait.Status.SessionType,
			wait.Status.InGame,
		),
		AppID:  appID,
		Status: wait.Status,
	}, nil
}

type RunConsoleCommandOutput struct {
	Success bool   `json:"success"   jsonschema:"Whether the command was executed successfully"`
	Message string `json:"message"   jsonschema:"Human-readable result message"`
	PID     uint32 `json:"pid"       jsonschema:"The process ID the command was sent to"`
	Command string `json:"command"   jsonschema:"The command that was executed"`
}

func handleRunConsoleCommand(ctx context.Context, req *mcp.CallToolRequest, input RunConsoleCommandInput) (
	*mcp.CallToolResult, RunConsoleCommandOutput, error,
) {
	cmd := strings.TrimSpace(input.Command)
	if cmd == "" {
		return nil, RunConsoleCommandOutput{
			Success: false,
			Message: "Command cannot be empty",
		}, nil
	}

	conn, err := connectToGMod(input.processTarget)
	if err != nil {
		return nil, RunConsoleCommandOutput{
			Success: false,
			Message: fmt.Sprintf("Failed to connect to Garry's Mod: %v", err),
			Command: cmd,
		}, nil
	}
	defer conn.Close()

	if err := conn.RunConsoleCommand(cmd); err != nil {
		return nil, RunConsoleCommandOutput{
			Success: false,
			Message: fmt.Sprintf("Failed to execute command: %v", err),
			PID:     conn.PID(),
			Command: cmd,
		}, nil
	}

	return nil, RunConsoleCommandOutput{
		Success: true,
		Message: fmt.Sprintf("Command '%s' executed successfully in Garry's Mod (PID: %d)", cmd, conn.PID()),
		PID:     conn.PID(),
		Command: cmd,
	}, nil
}

type GetConsoleOutputInput struct {
	Lines int `json:"lines,omitempty" jsonschema:"Maximum number of recent lines to return (default: 100, max: 1000)"`
	processTarget
}

type GetConsoleOutputOutput struct {
	Success bool   `json:"success"   jsonschema:"Whether the console output was retrieved successfully"`
	Message string `json:"message"   jsonschema:"Human-readable result message"`
	PID     uint32 `json:"pid"       jsonschema:"The process ID the command was sent to"`
	Lines   int    `json:"lines"     jsonschema:"Number of lines returned"`
	Content string `json:"content"   jsonschema:"Console output text content"`
}

func handleGetConsoleOutput(ctx context.Context, req *mcp.CallToolRequest, input GetConsoleOutputInput) (
	*mcp.CallToolResult, GetConsoleOutputOutput, error,
) {
	maxLines := input.Lines
	if maxLines <= 0 {
		maxLines = 100
	}
	if maxLines > 1000 {
		maxLines = 1000
	}

	conn, err := connectToGMod(input.processTarget)
	if err != nil {
		return nil, GetConsoleOutputOutput{
			Success: false,
			Message: fmt.Sprintf("Failed to connect to Garry's Mod: %v", err),
		}, nil
	}
	defer conn.Close()

	allLines, err := conn.ReadConsoleLines()
	if err != nil {
		return nil, GetConsoleOutputOutput{
			Success: false,
			Message: fmt.Sprintf("Failed to read console from process memory: %v", err),
			PID:     conn.PID(),
		}, nil
	}

	total := len(allLines)
	picked := gmod.RecentConsoleLines(allLines, maxLines)

	return nil, GetConsoleOutputOutput{
		Success: true,
		Message: fmt.Sprintf(
			"Read %d console lines from engine.dll memory (showing newest %d of %d total, PID %d)",
			len(picked), len(picked), total, conn.PID(),
		),
		PID:     conn.PID(),
		Lines:   len(picked),
		Content: strings.Join(picked, "\n"),
	}, nil
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "GetGameStatus",
		Description: "Check whether Garry's Mod is open and ready. " +
			"Also reports whether the client is currently in-game using IVEngineClient::IsInGame, " +
			"and classifies the current session as menu, local, server, or unknown.",
	}, handleGetGameStatus)

	mcp.AddTool(server, &mcp.Tool{
		Name: "LaunchGame",
		Description: "Ask Steam to launch Garry's Mod using the steam://run/4000 URI. " +
			"This does not launch gmod.exe directly because Garry's Mod should be started through Steam. " +
			"The call waits until the main game process is ready or the timeout is reached.",
	}, handleLaunchGame)

	mcp.AddTool(server, &mcp.Tool{
		Name: "RunConsoleCommand",
		Description: "Execute a console command in a running Garry's Mod (Source Engine) process. " +
			"Uses remote thread injection to call ClientCmd on the IVEngineClient015 interface. " +
			"Use this to run any Source Engine console command such as 'say Hello', 'sv_cheats 1', " +
			"'bot_add', 'changelevel gm_construct', etc.",
	}, handleRunConsoleCommand)

	mcp.AddTool(server, &mcp.Tool{
		Name: "GetConsoleOutput",
		Description: "Read the developer console history directly from Garry's Mod process memory. " +
			"Walks the engine.dll linked-list used when the engine dumps '-Console Buffer-' (same structure as sub_101F38C0). " +
			"Returns the newest N lines in chronological order. Offsets are tied to the current engine.dll build and may need updating after game patches.",
	}, handleGetConsoleOutput)

	log.Printf("Starting %s MCP server (stdio transport)...", serverName)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
