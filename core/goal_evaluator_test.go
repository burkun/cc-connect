package core

import (
	"context"
	"strings"
	"testing"
	"time"
)

// --- stub platform for goal tests ---
type stubGoalPlatform struct {
	n    string
	sent []string
}

func (p *stubGoalPlatform) Name() string { return p.n }
func (p *stubGoalPlatform) Start(handler MessageHandler) error { return nil }
func (p *stubGoalPlatform) Stop() error { return nil }
func (p *stubGoalPlatform) Reply(ctx context.Context, replyCtx any, content string) error {
	p.sent = append(p.sent, content)
	return nil
}
func (p *stubGoalPlatform) Send(ctx context.Context, replyCtx any, content string) error { return nil }

// --- goal command tests ---

func TestCLIGoalEvaluatorDefaults(t *testing.T) {
	evaluator := NewCLIGoalEvaluator("claude", "/tmp")
	if evaluator.Timeout != 60*time.Second {
		t.Errorf("expected Timeout 60s, got %v", evaluator.Timeout)
	}
}

func TestParseEvalResult(t *testing.T) {
	// Test continue=true means keep working
	result, err := parseEvalResult(`{"continue": true, "reason": "tests still failing"}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if result.Met {
		t.Errorf("expected Met=false for continue=true")
	}
	if result.Reason != "tests still failing" {
		t.Errorf("expected reason, got %q", result.Reason)
	}

	// Test continue=false means goal met
	result, err = parseEvalResult(`{"continue": false, "reason": "all tests passed"}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !result.Met {
		t.Errorf("expected Met=true for continue=false")
	}

	// Test impossible flag
	result, err = parseEvalResult(`{"continue": true, "impossible": true, "reason": "cannot proceed"}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !result.Impossible {
		t.Errorf("expected Impossible=true")
	}

	// Test JSON in markdown code block
	result, err = parseEvalResult("```json\n{\"continue\": false, \"reason\": \"done\"}\n```")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !result.Met {
		t.Errorf("expected Met=true from markdown block")
	}
}

func TestExtractCLIResultText(t *testing.T) {
	// Test single result object
	text := extractCLIResultText(`{"result": "goal is met"}`)
	if text != "goal is met" {
		t.Errorf("expected 'goal is met', got %q", text)
	}

	// Test array with result type
	text = extractCLIResultText(`[{"type": "assistant", "content": "thinking..."}, {"type": "result", "result": "done"}]`)
	if text != "done" {
		t.Errorf("expected 'done', got %q", text)
	}

	// Test array with content blocks
	text = extractCLIResultText(`[{"type": "assistant", "content": [{"type": "text", "text": "the answer"}]}]`)
	if text != "the answer" {
		t.Errorf("expected 'the answer', got %q", text)
	}

	// Test raw fallback
	text = extractCLIResultText(`plain text response`)
	if text != "plain text response" {
		t.Errorf("expected 'plain text response', got %q", text)
	}
}

// --- cmdGoal tests ---

func TestCmdGoal_Start(t *testing.T) {
	p := &stubGoalPlatform{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetGoalMaxTurns(10)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	result := e.cmdGoal(p, msg, []string{"fix", "all", "tests"})

	if result {
		t.Errorf("expected passthrough (false), got true")
	}

	if len(p.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "Goal mode activated") {
		t.Errorf("expected goal start message, got %q", p.sent[0])
	}

	state := e.getGoalState("test:user1")
	if state == nil || !state.active {
		t.Errorf("expected active goal state")
	}
	if state.condition != "fix all tests" {
		t.Errorf("expected condition 'fix all tests', got %q", state.condition)
	}
}

func TestCmdGoal_Status(t *testing.T) {
	p := &stubGoalPlatform{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	result := e.cmdGoalStatus(p, msg)
	if !result {
		t.Errorf("expected true (handled), got false")
	}
	if !strings.Contains(p.sent[len(p.sent)-1], "No goal") {
		t.Errorf("expected 'no goal' message, got %q", p.sent[len(p.sent)-1])
	}

	e.initGoalState("test:user1", "fix the bug")

	result = e.cmdGoalStatus(p, msg)
	if !result {
		t.Errorf("expected true (handled), got false")
	}
	if !strings.Contains(p.sent[len(p.sent)-1], "Goal:") {
		t.Errorf("expected goal status message, got %q", p.sent[len(p.sent)-1])
	}
}

func TestCmdGoal_Clear(t *testing.T) {
	p := &stubGoalPlatform{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	result := e.cmdGoalClear(p, msg)
	if !result {
		t.Errorf("expected true (handled), got false")
	}
	if !strings.Contains(p.sent[len(p.sent)-1], "No goal") {
		t.Errorf("expected 'no goal' message, got %q", p.sent[len(p.sent)-1])
	}

	e.initGoalState("test:user1", "fix the bug")

	result = e.cmdGoalClear(p, msg)
	if !result {
		t.Errorf("expected true (handled), got false")
	}
	if !strings.Contains(p.sent[len(p.sent)-1], "aborted") {
		t.Errorf("expected 'aborted' message, got %q", p.sent[len(p.sent)-1])
	}

	state := e.getGoalState("test:user1")
	if state != nil {
		t.Errorf("expected nil goal state after clear, got %+v", state)
	}
}

func TestGoalState_Management(t *testing.T) {
	p := &stubGoalPlatform{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetGoalMaxTurns(5)

	e.initGoalState("test:user1", "write tests")
	state := e.getGoalState("test:user1")
	if state == nil {
		t.Fatal("expected non-nil goal state")
	}
	if state.condition != "write tests" {
		t.Errorf("expected condition 'write tests', got %q", state.condition)
	}
	if state.maxTurns != 5 {
		t.Errorf("expected maxTurns 5, got %d", state.maxTurns)
	}
	if state.iterations != 0 {
		t.Errorf("expected iterations 0, got %d", state.iterations)
	}
	if !state.active {
		t.Errorf("expected active=true")
	}

	e.clearGoalState("test:user1")
	state = e.getGoalState("test:user1")
	if state != nil {
		t.Errorf("expected nil after clear, got %+v", state)
	}
}