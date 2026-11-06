//go:build windows && 386

// runconsole is a small CLI to verify RunConsoleCommand without starting the MCP server.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"gmod_mcp/internal/gmod"
)

func main() {
	var pid uint64
	var process string
	var timeout time.Duration
	flag.Uint64Var(&pid, "pid", 0, "Garry's Mod PID (0 = auto)")
	flag.StringVar(&process, "process", "", "Process name (default: gmod.exe)")
	flag.DurationVar(&timeout, "timeout", 5*time.Second, "Maximum wait for engine execution confirmation and console output")
	flag.Parse()

	command := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if command == "" {
		fmt.Fprintln(os.Stderr, "usage: runconsole [flags] <console command>")
		os.Exit(2)
	}

	conn, err := gmod.Connect(uint32(pid), process)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	result, err := conn.ExecuteConsoleCommand(context.Background(), command, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "RunConsoleCommand: %v\n", err)
		os.Exit(1)
	}

	if result.Output != "" {
		fmt.Println(result.Output)
	}
	fmt.Fprintf(os.Stderr, "%s in pid=%d: %s\n", result.Status, conn.PID(), command)
	if result.CaptureError != "" {
		fmt.Fprintln(os.Stderr, result.CaptureError)
	}
	if result.Status != "executed" {
		os.Exit(1)
	}
}
