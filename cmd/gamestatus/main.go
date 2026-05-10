//go:build windows && 386

// gamestatus verifies game status detection without starting the MCP server.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"gmod_mcp/internal/gmod"
)

func main() {
	var process string
	flag.StringVar(&process, "process", "", "Process name (default: gmod.exe)")
	flag.Parse()

	status, err := gmod.Status(process)
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(status); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
}
