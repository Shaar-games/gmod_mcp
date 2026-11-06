//go:build windows && 386

package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gmod_mcp/internal/gmod"
)

func TestGameEventsMCPDeliveryAndFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, &mcp.ServerOptions{
		SubscribeHandler:   validateGameEventsSubscription,
		UnsubscribeHandler: func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
	})
	history := &gmod.EventHistory{}
	registerGameEvents(server, history)
	logs := make(chan *mcp.LoggingMessageParams, 4)
	updates := make(chan string, 4)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, &mcp.ClientOptions{
		LoggingMessageHandler: func(_ context.Context, req *mcp.LoggingMessageRequest) { logs <- req.Params },
		ResourceUpdatedHandler: func(_ context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
			updates <- req.Params.URI
		},
	})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Logging is opt-in in this SDK; events must still be retrievable.
	publishGameEvent(ctx, server, history, gmod.GameEvent{Type: "game_stopped", PID: 123})
	readEvents := func(afterID uint64) GetGameEventsOutput {
		t.Helper()
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "GetGameEvents", Arguments: map[string]any{"afterId": afterID}})
		if err != nil || result.IsError {
			t.Fatalf("GetGameEvents: %v, result=%+v", err, result)
		}
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var output GetGameEventsOutput
		if err := json.Unmarshal(data, &output); err != nil {
			t.Fatal(err)
		}
		return output
	}
	if output := readEvents(0); len(output.Events) != 1 || output.LastEventID != 1 {
		t.Fatalf("fallback history: %+v", output)
	}
	select {
	case event := <-logs:
		t.Fatalf("unexpected opt-out log: %+v", event)
	default:
	}
	if err := cs.Subscribe(ctx, &mcp.SubscribeParams{URI: "gmod://unknown"}); err == nil {
		t.Fatal("unknown resource subscription was accepted")
	}
	if err := cs.Subscribe(ctx, &mcp.SubscribeParams{URI: gameEventsURI}); err != nil {
		t.Fatal(err)
	}
	if err := cs.SetLoggingLevel(ctx, &mcp.SetLoggingLevelParams{Level: "notice"}); err != nil {
		t.Fatal(err)
	}
	publishGameEvent(ctx, server, history, gmod.GameEvent{Type: "game_stopped", PID: 124})
	select {
	case event := <-logs:
		if event.Level != "notice" || event.Logger != "gmod.lifecycle" {
			t.Fatalf("incorrect log payload: %+v", event)
		}
	case <-ctx.Done():
		t.Fatal("stop notification never arrived")
	}
	select {
	case uri := <-updates:
		if uri != gameEventsURI {
			t.Fatal(uri)
		}
	case <-ctx.Done():
		t.Fatal("resource update never arrived")
	}
	resource, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: gameEventsURI})
	if err != nil || len(resource.Contents) != 1 {
		t.Fatalf("read events resource: %v", err)
	}
	var resourceOutput GetGameEventsOutput
	if err := json.Unmarshal([]byte(resource.Contents[0].Text), &resourceOutput); err != nil || len(resourceOutput.Events) != 2 {
		t.Fatalf("resource history: %+v, err=%v", resourceOutput, err)
	}
	if err := cs.Unsubscribe(ctx, &mcp.UnsubscribeParams{URI: gameEventsURI}); err != nil {
		t.Fatal(err)
	}
	if err := cs.SetLoggingLevel(ctx, &mcp.SetLoggingLevelParams{Level: "error"}); err != nil {
		t.Fatal(err)
	}
	publishGameEvent(ctx, server, history, gmod.GameEvent{Type: "game_stopped", PID: 125})
	publishGameEvent(ctx, server, history, gmod.GameEvent{
		Type: "game_crashed", PID: 126, ExitCode: 0xc0000005, Console: "last console line",
		CrashFiles: []gmod.CrashFile{{Path: "crash.log", Text: "fatal error"}},
	})
	select {
	case event := <-logs:
		if event.Level != "error" {
			t.Fatalf("log filtering failed: %+v", event)
		}
		data, _ := json.Marshal(event.Data)
		var crash gmod.GameEvent
		if err := json.Unmarshal(data, &crash); err != nil || crash.ID != 4 || crash.Console != "last console line" || len(crash.CrashFiles) != 1 || crash.CrashFiles[0].Text != "fatal error" {
			t.Fatalf("crash diagnostics lost: %+v err=%v", crash, err)
		}
	case <-ctx.Done():
		t.Fatal("crash notification never arrived")
	}
	if output := readEvents(2); len(output.Events) != 2 || output.LastEventID != 4 {
		t.Fatalf("cursor filtering: %+v", output)
	}
	select {
	case uri := <-updates:
		t.Fatalf("received resource update after unsubscribe: %s", uri)
	default:
	}
	if err := cs.SetLoggingLevel(ctx, &mcp.SetLoggingLevelParams{Level: "notice"}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		kind  string
		level mcp.LoggingLevel
	}{
		{"game_ready", "notice"}, {"game_unresponsive", "warning"}, {"game_responsive", "notice"},
	} {
		publishGameEvent(ctx, server, history, gmod.GameEvent{Type: test.kind, PID: 123})
		select {
		case event := <-logs:
			if event.Level != test.level {
				t.Fatalf("%s severity=%s", test.kind, event.Level)
			}
		case <-ctx.Done():
			t.Fatalf("%s notification never arrived", test.kind)
		}
	}
}

func TestCrashReportToolAndExperimentSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	registerCrashReport(server)
	registerExperiment(server, &gmod.EventHistory{})
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
	path := os.Getenv("GMOD_MINIDUMP_SAMPLE")
	if path == "" {
		t.Skip("set GMOD_MINIDUMP_SAMPLE to test a real dump via MCP")
	}
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "GetCrashReport", Arguments: map[string]any{"path": path}})
	if err != nil || result.IsError {
		t.Fatalf("GetCrashReport: %v, result=%+v", err, result)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output GetCrashReportOutput
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	if !output.Success || output.Summary == nil || output.Summary.ExceptionCode != 0xc0000005 {
		t.Fatalf("wrong crash summary: %+v", output)
	}
}
