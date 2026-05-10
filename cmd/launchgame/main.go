//go:build windows && 386

// launchgame verifies Steam launch + wait-for-ready without starting the MCP server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"gmod_mcp/internal/gmod"
)

type output struct {
	Success  bool            `json:"success"`
	Message  string          `json:"message"`
	TimedOut bool            `json:"timedOut"`
	Status   gmod.GameStatus `json:"status"`
}

func main() {
	var timeoutSeconds int
	flag.IntVar(&timeoutSeconds, "timeout", 90, "Maximum seconds to wait for Garry's Mod to be ready")
	flag.Parse()

	if timeoutSeconds <= 0 {
		timeoutSeconds = 90
	}
	if timeoutSeconds > 600 {
		timeoutSeconds = 600
	}

	if err := gmod.LaunchViaSteam(4000); err != nil {
		printOutput(output{
			Success: false,
			Message: fmt.Sprintf("failed to request Steam launch: %v", err),
		})
		os.Exit(1)
	}

	wait, err := gmod.WaitForReady(context.Background(), "", time.Duration(timeoutSeconds)*time.Second, time.Second)
	if err != nil {
		printOutput(output{
			Success: false,
			Message: fmt.Sprintf("Steam launch requested, but status check failed: %v", err),
			Status:  wait.Status,
		})
		os.Exit(1)
	}
	if wait.TimedOut {
		printOutput(output{
			Success:  false,
			Message:  fmt.Sprintf("Steam launch requested, but Garry's Mod was not ready within %d seconds", timeoutSeconds),
			TimedOut: true,
			Status:   wait.Status,
		})
		os.Exit(2)
	}

	printOutput(output{
		Success: true,
		Message: fmt.Sprintf("Garry's Mod is ready (session: %s, inGame: %t)", wait.Status.SessionType, wait.Status.InGame),
		Status:  wait.Status,
	})
}

func printOutput(out output) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}
