package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

// TestOpencodeSessionEntry_Unmarshal verifies that OpenCode's
// `session list --format json` output can be correctly parsed.
//
// OpenCode returns `updated` and `created` as Unix timestamps in
// milliseconds (int64), not strings. This test prevents regression
// of the unmarshal error:
//
//	json: cannot unmarshal number into Go struct field opencodeSessionEntry.updated of type string
func TestOpencodeSessionEntry_Unmarshal(t *testing.T) {
	jsonData := `[
  {
    "id": "ses_2eb11bb11ffeYwQZOj25mlmGMc",
    "title": "Test Session",
    "updated": 1774174646445,
    "created": 1774172652782,
    "projectId": "b80385ead03e8b450bdb2016d434aad318f93c16",
    "directory": "/path/to/project"
  }
]`

	var entries []opencodeSessionEntry
	if err := json.Unmarshal([]byte(jsonData), &entries); err != nil {
		t.Fatalf("Failed to unmarshal OpenCode session list: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.ID != "ses_2eb11bb11ffeYwQZOj25mlmGMc" {
		t.Errorf("ID = %q, want %q", e.ID, "ses_2eb11bb11ffeYwQZOj25mlmGMc")
	}
	if e.Title != "Test Session" {
		t.Errorf("Title = %q, want %q", e.Title, "Test Session")
	}
	if e.Updated != 1774174646445 {
		t.Errorf("Updated = %d, want %d", e.Updated, 1774174646445)
	}
	if e.Created != 1774172652782 {
		t.Errorf("Created = %d, want %d", e.Created, 1774172652782)
	}
}

// TestNewOpencodeSession_ContinueSessionTreatedAsFresh verifies that
// the ContinueSession sentinel (__continue__) is not passed as a literal
// session ID to the CLI. This was fixed in PR #249.
func TestNewOpencodeSession_ContinueSessionTreatedAsFresh(t *testing.T) {
	s, err := newOpencodeSession(context.Background(), "echo", nil, "/tmp", "", "default", "", core.ContinueSession, nil, 0)
	if err != nil {
		t.Fatalf("newOpencodeSession: %v", err)
	}
	defer s.Close()

	if got := s.CurrentSessionID(); got != "" {
		t.Errorf("ContinueSession should be treated as fresh: chatID = %q, want empty", got)
	}
}

func TestOpencodeSessionStageImages(t *testing.T) {
	dir := t.TempDir()
	s := &opencodeSession{workDir: dir}

	prompt, imagePaths, err := s.stageImages("", []core.ImageAttachment{
		{MimeType: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff}},
		{MimeType: "image/webp", Data: []byte("webp")},
	})
	if err != nil {
		t.Fatalf("stageImages: %v", err)
	}
	if prompt != "Please analyze the attached image(s)." {
		t.Fatalf("prompt = %q", prompt)
	}
	if len(imagePaths) != 2 {
		t.Fatalf("imagePaths len = %d, want 2", len(imagePaths))
	}
	if filepath.Ext(imagePaths[0]) != ".jpg" {
		t.Fatalf("first ext = %q, want .jpg", filepath.Ext(imagePaths[0]))
	}
	if filepath.Ext(imagePaths[1]) != ".webp" {
		t.Fatalf("second ext = %q, want .webp", filepath.Ext(imagePaths[1]))
	}
	for _, path := range imagePaths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected staged image %s: %v", path, err)
		}
	}
}

func TestOpencodeSessionBuildRunArgsIncludesImagesAsFiles(t *testing.T) {
	s := &opencodeSession{workDir: "/repo", model: "provider/model"}

	got := s.buildRunArgs("describe these images", []string{"/tmp/a.png", "/tmp/b.jpg"}, "ses_123")
	want := []string{
		"run", "--format", "json",
		"--session", "ses_123",
		"--model", "provider/model",
		"--dir", "/repo",
		"--thinking",
		"--file", "/tmp/a.png",
		"--file", "/tmp/b.jpg",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

// TestOpencodeBuildRunArgsV1KeepsDirFlag guards the legacy (opencode 1.x)
// argument set, which requires --dir because the CLI does not derive the
// project directory from the process working directory.
func TestOpencodeBuildRunArgsV1KeepsDirFlag(t *testing.T) {
	s := &opencodeSession{workDir: "/repo", model: "provider/model", mode: "yolo", cliMajor: 1}

	got := s.buildRunArgs("hi", nil, "")
	want := []string{
		"run", "--format", "json",
		"--model", "provider/model",
		"--dir", "/repo",
		"--thinking",
		"--dangerously-skip-permissions",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

// TestOpencodeBuildRunArgsV2OmitsDirFlag is the regression guard for the
// opencode 2.x breakage: v2 removed `--dir` and now rejects it with
// "Unrecognized flag: --dir in command opencode run". The project directory is
// taken from the process working directory instead.
func TestOpencodeBuildRunArgsV2OmitsDirFlag(t *testing.T) {
	s := &opencodeSession{workDir: "/repo", model: "provider/model", cliMajor: 2}

	got := s.buildRunArgs("hi", nil, "")

	for i, a := range got {
		if a == "--dir" {
			t.Fatalf("args contain --dir (index %d): %#v", i, got)
		}
	}
	want := []string{
		"run", "--format", "json",
		"--model", "provider/model",
		"--thinking",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

// TestOpencodeBuildRunArgsV2YoloUsesAuto verifies that yolo mode maps to the
// v2 `--auto` flag instead of the removed `--dangerously-skip-permissions`.
func TestOpencodeBuildRunArgsV2YoloUsesAuto(t *testing.T) {
	s := &opencodeSession{workDir: "/repo", mode: "yolo", cliMajor: 2}

	got := s.buildRunArgs("hi", nil, "")
	want := []string{"run", "--format", "json", "--thinking", "--auto"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

// TestParseMajorVersion covers the opencode --version output shapes we need to
// distinguish v1 (legacy flags) from v2 (new flags).
func TestParseMajorVersion(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"opencode v2.0.32\n", 2},
		{"opencode v2.0.32", 2},
		{"1.5.1-beta.1", 1},
		{"opencode 1.4.2", 1},
		{"v2", 2},
		{"no digits here", 0},
		{"", 0},
	}
	for _, tc := range cases {
		if got := parseMajorVersion(tc.in); got != tc.want {
			t.Errorf("parseMajorVersion(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestDetectOpencodeMajorVersion probes a stub CLI on disk, mirroring how New()
// decides between v1 and v2 argument sets.
func TestDetectOpencodeMajorVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is not portable to Windows")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "opencode")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'opencode v2.0.32'\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	if got := detectOpencodeMajorVersion(script); got != 2 {
		t.Errorf("detectOpencodeMajorVersion(stub v2) = %d, want 2", got)
	}

	if got := detectOpencodeMajorVersion(filepath.Join(dir, "missing-opencode")); got != 0 {
		t.Errorf("detectOpencodeMajorVersion(missing) = %d, want 0", got)
	}
}

// TestHandleEventCapturesSessionIDWithoutStepStart reproduces opencode 2.x
// behavior where a turn can emit only a text event (no step_start). The session
// ID must still be captured, otherwise --session resume breaks and every user
// message starts a brand new conversation.
func TestHandleEventCapturesSessionIDWithoutStepStart(t *testing.T) {
	jsonData := `{"type":"text","sessionID":"ses_v2_only","part":{"type":"text","text":"PONG"}}`

	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonData), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &opencodeSession{events: make(chan core.Event, 4), ctx: ctx}
	s.handleEvent(raw)

	if got := s.CurrentSessionID(); got != "ses_v2_only" {
		t.Errorf("sessionID = %q, want %q (captured from non-step_start event)", got, "ses_v2_only")
	}
}

// TestHandleStepStart_SessionIDFromTopLevel verifies that handleStepStart
// prefers the sessionID from the top-level JSON field when both top-level
// and part-level sessionID are present. This matches OpenCode's stdout format.
func TestHandleStepStart_SessionIDFromTopLevel(t *testing.T) {
	jsonData := `{"type":"step_start","sessionID":"ses_top_level","part":{"sessionID":"ses_part_level"}}`

	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonData), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	s := &opencodeSession{}
	s.handleStepStart(raw)

	if got := s.CurrentSessionID(); got != "ses_top_level" {
		t.Errorf("sessionID = %q, want %q (should prefer top-level)", got, "ses_top_level")
	}
}

// TestHandleStepStart_SessionIDFromPart verifies that handleStepStart
// falls back to the sessionID inside part when top-level sessionID is absent.
func TestHandleStepStart_SessionIDFromPart(t *testing.T) {
	jsonData := `{"type":"step_start","part":{"sessionID":"ses_part_level"}}`

	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonData), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	s := &opencodeSession{}
	s.handleStepStart(raw)

	if got := s.CurrentSessionID(); got != "ses_part_level" {
		t.Errorf("sessionID = %q, want %q (should fallback to part)", got, "ses_part_level")
	}
}

// TestHandleStepStopSendsEventResult verifies that handleStepFinish sends
// an EventResult when reason="stop", signaling turn completion to the engine.
func TestHandleStepStopSendsEventResult(t *testing.T) {
	jsonData := `{"type":"step_finish","part":{"reason":"stop"}}`

	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonData), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &opencodeSession{events: make(chan core.Event, 1), ctx: ctx}
	s.handleStepFinish(raw)

	select {
	case evt := <-s.events:
		if evt.Type != core.EventResult {
			t.Errorf("event type = %q, want EventResult", evt.Type)
		}
		if !evt.Done {
			t.Errorf("event.Done = false, want true")
		}
	default:
		t.Error("expected EventResult to be sent when reason=stop")
	}
}

// TestHandleStepToolCallsNoEventResult verifies that handleStepFinish does NOT
// send EventResult when reason="tool-calls", allowing the agent to continue
// with subsequent tool execution steps.
func TestHandleStepToolCallsNoEventResult(t *testing.T) {
	jsonData := `{"type":"step_finish","part":{"reason":"tool-calls"}}`

	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonData), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &opencodeSession{events: make(chan core.Event, 1), ctx: ctx}
	s.handleStepFinish(raw)

	select {
	case evt := <-s.events:
		t.Errorf("unexpected event sent when reason=tool-calls: %v", evt)
	default:
	}
}

// TestHandleStepDuplicateEventResultPrevented verifies that calling
// handleStepFinish multiple times with reason="stop" only sends one
// EventResult, preventing duplicate completion signals to the engine.
func TestHandleStepDuplicateEventResultPrevented(t *testing.T) {
	jsonData := `{"type":"step_finish","part":{"reason":"stop"}}`

	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonData), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &opencodeSession{
		events:     make(chan core.Event, 2),
		ctx:        ctx,
		resultSent: atomic.Bool{},
	}

	s.handleStepFinish(raw)
	s.handleStepFinish(raw)

	count := 0
	for len(s.events) > 0 {
		evt := <-s.events
		if evt.Type == core.EventResult {
			count++
		}
	}

	if count != 1 {
		t.Errorf("EventResult count = %d, want 1 (duplicate should be prevented)", count)
	}
}

// TestHandleToolUsePermissionDeniedEmitsEventText verifies that when opencode
// rejects a tool call (status="error"), the error message is emitted as an
// EventText so the engine has something meaningful to deliver instead of
// the generic "(空响应)" / "(empty response)" placeholder.
// Reproduces the scenario in issue #178 where running bash commands in
// default mode silently produced an empty response.
func TestHandleToolUsePermissionDeniedEmitsEventText(t *testing.T) {
	jsonData := `{"type":"tool_use","part":{"tool":"bash","state":{"status":"error","error":"The user rejected permission to use this specific tool call.","input":{"command":"ls","description":"List files in current directory"}}}}`

	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonData), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &opencodeSession{events: make(chan core.Event, 4), ctx: ctx}
	s.handleToolUse(raw)

	var events []core.Event
	for len(s.events) > 0 {
		events = append(events, <-s.events)
	}

	if len(events) < 2 {
		t.Fatalf("expected at least 2 events (EventToolUse + EventText), got %d: %v", len(events), events)
	}
	if events[0].Type != core.EventToolUse {
		t.Errorf("events[0].Type = %v, want EventToolUse", events[0].Type)
	}
	if events[1].Type != core.EventText {
		t.Errorf("events[1].Type = %v, want EventText (error text so engine has content)", events[1].Type)
	}
	if !strings.Contains(events[1].Content, "rejected permission") {
		t.Errorf("EventText.Content = %q, want it to contain the rejection reason", events[1].Content)
	}
}

// TestHandleToolUseCompletedDoesNotEmitExtraText verifies that a successfully
// completed tool call does NOT emit an EventText (regression guard).
func TestHandleToolUseCompletedDoesNotEmitExtraText(t *testing.T) {
	jsonData := `{"type":"tool_use","part":{"tool":"bash","state":{"status":"completed","output":"file1.txt file2.txt","input":{"command":"ls"}}}}`

	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonData), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &opencodeSession{events: make(chan core.Event, 4), ctx: ctx}
	s.handleToolUse(raw)

	var events []core.Event
	for len(s.events) > 0 {
		events = append(events, <-s.events)
	}

	for _, evt := range events {
		if evt.Type == core.EventText {
			t.Errorf("unexpected EventText for completed tool: %q", evt.Content)
		}
	}
	if len(events) < 2 {
		t.Errorf("expected EventToolUse + EventToolResult for completed tool, got %d events", len(events))
	}
}

// TestHandleToolUseErrorNoMessageNoText verifies that a tool error with empty
// error message does NOT emit a spurious empty EventText.
func TestHandleToolUseErrorNoMessageNoText(t *testing.T) {
	jsonData := `{"type":"tool_use","part":{"tool":"bash","state":{"status":"error"}}}`

	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonData), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &opencodeSession{events: make(chan core.Event, 4), ctx: ctx}
	s.handleToolUse(raw)

	for len(s.events) > 0 {
		evt := <-s.events
		if evt.Type == core.EventText {
			t.Errorf("unexpected EventText for error with no message: %q", evt.Content)
		}
	}
}

// verify Agent implements core.Agent
var _ core.Agent = (*Agent)(nil)
