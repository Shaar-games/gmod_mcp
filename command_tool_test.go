//go:build windows && 386

package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gmod_mcp/internal/gmod"
)

type stubConsoleCommander struct {
	result gmod.CommandResult
	err    error
}

func (s stubConsoleCommander) PID() uint32 { return 42 }
func (s stubConsoleCommander) ExecuteConsoleCommand(context.Context, string, time.Duration) (gmod.CommandResult, error) {
	return s.result, s.err
}

func TestConsoleCommandMCPReportsExecutionErrorsAndUncertainty(t *testing.T) {
	for _, test := range []struct {
		name      string
		commander stubConsoleCommander
		success   bool
	}{
		{"executed", stubConsoleCommander{result: gmod.CommandResult{Status: "executed", ExecutionConfirmed: true, Output: "command output", Errors: []string{}}}, true},
		{"engine error", stubConsoleCommander{result: gmod.CommandResult{Status: "error", ExecutionConfirmed: true, Output: `Unknown command "test"`, Errors: []string{`Unknown command "test"`}}}, false},
		{"unconfirmed", stubConsoleCommander{result: gmod.CommandResult{Status: "unconfirmed", TimedOut: true, CaptureError: "missing end marker", Errors: []string{}}}, false},
		{"submission failed", stubConsoleCommander{result: gmod.CommandResult{Status: "unconfirmed", Errors: []string{}}, err: errors.New("engine call failed")}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
			mcp.AddTool(server, &mcp.Tool{Name: "RunConsoleCommand"}, func(ctx context.Context, _ *mcp.CallToolRequest, input RunConsoleCommandInput) (*mcp.CallToolResult, RunConsoleCommandOutput, error) {
				return commandToolResponse(runConfirmedConsoleCommand(ctx, test.commander, input.Command, time.Second))
			})
			a, b := mcp.NewInMemoryTransports()
			ss, err := server.Connect(ctx, a, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
			cs, err := client.Connect(ctx, b, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "RunConsoleCommand", Arguments: map[string]any{"command": "test"}})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError == test.success {
				t.Fatalf("incorrect MCP isError=%t", result.IsError)
			}
			data, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var output RunConsoleCommandOutput
			if err := json.Unmarshal(data, &output); err != nil {
				t.Fatal(err)
			}
			if output.Success != test.success || output.Status != test.commander.result.Status || output.Output != test.commander.result.Output || output.PID != 42 {
				t.Fatalf("incorrect structured result: %+v", output)
			}
			if test.commander.err != nil && output.CaptureError == "" {
				t.Fatal("submission failure reason was lost")
			}
		})
	}
}
