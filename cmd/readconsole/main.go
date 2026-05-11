//go:build windows && 386

// readconsole is a small CLI to verify ReadConsoleLines without starting the MCP server.
//
// From repo root (386 binary required — Garry's Mod is 32-bit):
//
//	$env:GOOS="windows"; $env:GOARCH="386"; go run ./cmd/readconsole -lines 50
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
	var maxShow int
	var raw bool
	flag.Uint64Var(&pid, "pid", 0, "Garry's Mod PID (0 = auto)")
	flag.StringVar(&process, "process", "", "Process name (default: gmod.exe)")
	flag.IntVar(&maxShow, "lines", 100, "Print only the newest N lines (0 = all)")
	flag.BoolVar(&raw, "raw", false, "Print raw engine console buffer entries")
	flag.Parse()

	conn, err := gmod.Connect(uint32(pid), process)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	eb := conn.EngineBase()
	fmt.Fprintf(os.Stderr, "pid=%d client.dll=0x%X engine.dll=0x%X\n",
		conn.PID(), conn.ClientBase(), eb)

	rawNodes, rawChain, err := conn.EngineConsoleRawGlobals()
	if err != nil {
		fmt.Fprintf(os.Stderr, "EngineConsoleRawGlobals: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "debug: nodesTable pointer (remote dword)=0x%X chainWord=0x%X hiIdx=0x%X loIdx=0x%X\n",
		rawNodes, rawChain, (rawChain>>16)&0xffff, rawChain&0xffff)

	lines, err := conn.ReadConsoleLines()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ReadConsoleLines: %v\n", err)
		os.Exit(1)
	}

	total := len(lines)
	fmt.Fprintf(os.Stderr, "total lines in chain: %d\n", total)

	out := gmod.ConsoleDisplayLines(lines, maxShow)
	if raw {
		out = gmod.RecentConsoleLines(lines, maxShow)
	}

	fmt.Println(strings.Join(out, "\n"))
}
