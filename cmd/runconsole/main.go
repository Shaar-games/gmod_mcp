//go:build windows && 386

// runconsole is a small CLI to verify RunConsoleCommand without starting the MCP server.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"gmod_mcp/internal/gmod"
)

func main() {
	var pid uint64
	var process string
	flag.Uint64Var(&pid, "pid", 0, "Garry's Mod PID (0 = auto)")
	flag.StringVar(&process, "process", "", "Process name (default: gmod.exe)")
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

	if err := conn.RunConsoleCommand(command); err != nil {
		fmt.Fprintf(os.Stderr, "RunConsoleCommand: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "executed in pid=%d: %s\n", conn.PID(), command)
}
