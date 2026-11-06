//go:build windows && 386

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gmod_mcp/internal/gmod"
	"gmod_mcp/internal/minidump"
)

const gameEventsURI = "gmod://events"

type GetCrashReportInput struct {
	Path string `json:"path" jsonschema:"Path to a local .mdmp or .dmp Windows minidump"`
}

type GetCrashReportOutput struct {
	Success bool              `json:"success"`
	Message string            `json:"message"`
	Summary *minidump.Summary `json:"summary,omitempty"`
}

func registerCrashReport(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "GetCrashReport", Description: "Extract the exception, faulting module and relative offset, plus heuristic stack-address candidates, from an existing Windows minidump."},
		func(_ context.Context, _ *mcp.CallToolRequest, input GetCrashReportInput) (*mcp.CallToolResult, GetCrashReportOutput, error) {
			ext := strings.ToLower(filepath.Ext(input.Path))
			if ext != ".mdmp" && ext != ".dmp" {
				return &mcp.CallToolResult{IsError: true}, GetCrashReportOutput{Message: "Path must end in .mdmp or .dmp"}, nil
			}
			summary, err := minidump.ParseFile(input.Path)
			if err != nil {
				return &mcp.CallToolResult{IsError: true}, GetCrashReportOutput{Message: err.Error()}, nil
			}
			return nil, GetCrashReportOutput{Success: true, Message: "Minidump summary extracted", Summary: &summary}, nil
		})
}

type GetGameEventsInput struct {
	AfterID uint64 `json:"afterId,omitempty" jsonschema:"Return only events newer than this ID (default: all retained events)"`
}

type GetGameEventsOutput struct {
	Events      []gmod.GameEvent `json:"events" jsonschema:"Retained readiness, responsiveness and process exit events in chronological order"`
	LastEventID uint64           `json:"lastEventId" jsonschema:"Latest event ID; pass as afterId on the next call. IDs reset on MCP server restart; only the newest 32 events are retained"`
}

func validateGameEventsSubscription(_ context.Context, req *mcp.SubscribeRequest) error {
	if req.Params.URI != gameEventsURI {
		return fmt.Errorf("unknown event resource: %s", req.Params.URI)
	}
	return nil
}

func registerGameEvents(server *mcp.Server, history *gmod.EventHistory) {
	snapshot := func(afterID uint64) GetGameEventsOutput {
		events, lastID := history.Since(afterID)
		return GetGameEventsOutput{Events: events, LastEventID: lastID}
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "GetGameEvents",
		Description: "Read recent Garry's Mod readiness, responsiveness and stop/crash events, including available diagnostics. " +
			"Works when the client does not display MCP notifications. Only the newest 32 events are retained until the MCP server restarts.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input GetGameEventsInput) (*mcp.CallToolResult, GetGameEventsOutput, error) {
		return nil, snapshot(input.AfterID), nil
	})
	server.AddResource(&mcp.Resource{
		URI: gameEventsURI, Name: "Garry's Mod lifecycle events", MIMEType: "application/json",
		Description: "Recent readiness, responsiveness and stop/crash events. Subscribe for notifications/resources/updated.",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		data, err := json.Marshal(snapshot(0))
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: gameEventsURI, MIMEType: "application/json", Text: string(data),
		}}}, nil
	})
}

func publishGameEvent(ctx context.Context, server *mcp.Server, history *gmod.EventHistory, event gmod.GameEvent) {
	event = history.Add(event)
	level := mcp.LoggingLevel("notice")
	if event.Type == "game_crashed" {
		level = "error"
	} else if event.Type == "game_unresponsive" {
		level = "warning"
	}
	for session := range server.Sessions() {
		if session.InitializeParams() == nil {
			continue
		}
		notifyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := session.Log(notifyCtx, &mcp.LoggingMessageParams{
			Level: level, Logger: "gmod.lifecycle", Data: event,
		})
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("Send game event: %v", err)
		}
	}
	if err := server.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: gameEventsURI}); err != nil && ctx.Err() == nil {
		log.Printf("Notify game event resource: %v", err)
	}
}
