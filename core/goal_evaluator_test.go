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

func TestGoalEvaluatorTokenBudget(t *testing.T) {
	// Create evaluator with default 16k token budget
	evaluator := NewAnthropicGoalEvaluator(GoalEvaluatorCfg{
		APIKey: "test",
	})
	if evaluator.TokenBudget != 16000 {
		t.Errorf("expected TokenBudget 16000, got %d", evaluator.TokenBudget)
	}
}

func TestGoalEvaluatorBuildTranscript(t *testing.T) {
	evaluator := NewAnthropicGoalEvaluator(GoalEvaluatorCfg{APIKey: "test"})

	// Test empty history
	transcript := evaluator.buildTranscript(nil)
	if transcript != "" {
		t.Errorf("expected empty transcript for nil history, got %q", transcript)
	}

	// Test single entry
	history := []HistoryEntry{
		{Role: "user", Content: "Hello"},
	}
	transcript = evaluator.buildTranscript(history)
	if !strings.Contains(transcript, "[USER]: Hello") {
		t.Errorf("expected [USER]: Hello in transcript, got %q", transcript)
	}

	// Test truncation of long content (1500 char limit per entry)
	shortContent := strings.Repeat("x", 1000)
	history = []HistoryEntry{
		{Role: "assistant", Content: shortContent},
	}
	transcript = evaluator.buildTranscript(history)
	if strings.Contains(transcript, "...[truncated]") {
		t.Errorf("expected no truncation for 1000 chars, but got truncation")
	}

	longContent := strings.Repeat("x", 2000)
	history = []HistoryEntry{
		{Role: "assistant", Content: longContent},
	}
	transcript = evaluator.buildTranscript(history)
	// 1500 char limit should trigger truncation for 2000 chars
	if !strings.Contains(transcript, "...[truncated]") {
		t.Errorf("expected truncation for 2000 chars, got %q", transcript[:100])
	}

	// Test multiple entries preserve order
	history = []HistoryEntry{
		{Role: "user", Content: "First"},
		{Role: "assistant", Content: "Second"},
		{Role: "user", Content: "Third"},
	}
	transcript = evaluator.buildTranscript(history)
	// Should contain all entries in chronological order
	if !strings.Contains(transcript, "[USER]: First") ||
		!strings.Contains(transcript, "[ASSISTANT]: Second") ||
		!strings.Contains(transcript, "[USER]: Third") {
		t.Errorf("expected all entries in order, got %q", transcript)
	}
}

func TestGoalEvaluatorBuildTranscriptBudgetLimit(t *testing.T) {
	evaluator := NewAnthropicGoalEvaluator(GoalEvaluatorCfg{APIKey: "test"})

	// Create many entries that would exceed budget
	// With 16k budget, history gets ~15k tokens = ~60k chars
	// Each entry at max 1500 chars = 1500/4 = 375 tokens
	// So roughly 40 entries should fit in 15k tokens
	history := make([]HistoryEntry, 100)
	for i := 0; i < 100; i++ {
		history[i] = HistoryEntry{
			Role:    "user",
			Content: strings.Repeat("x", 1000), // 1000 chars = 250 tokens
		}
	}

	transcript := evaluator.buildTranscript(history)

	// Should have truncated due to budget
	if !strings.Contains(transcript, "skipped") {
		t.Errorf("expected budget truncation message, got transcript of length %d", len(transcript))
	}

	// Should not exceed budget (roughly)
	// Token budget 16k minus fixed overhead, say 15k for history = 60k chars
	// Allow some margin
	if len(transcript) > 70000 {
		t.Errorf("transcript exceeds expected budget: %d chars", len(transcript))
	}
}

func TestGoalEvaluatorBuildToolSection(t *testing.T) {
	evaluator := NewAnthropicGoalEvaluator(GoalEvaluatorCfg{APIKey: "test"})

	// Test empty tools
	section := evaluator.buildToolSection(nil)
	if section != "" {
		t.Errorf("expected empty section for nil tools, got %q", section)
	}

	// Test single tool
	tools := []ToolRecord{
		{Name: "Bash", Input: "ls -la", Result: "file1.txt\nfile2.txt", Success: true},
	}
	section = evaluator.buildToolSection(tools)
	if !strings.Contains(section, "Tool: Bash") ||
		!strings.Contains(section, "Status: success") ||
		!strings.Contains(section, "ls -la") {
		t.Errorf("expected tool info in section, got %q", section)
	}

	// Test failed tool
	tools = []ToolRecord{
		{Name: "Bash", Input: "false", Result: "", Success: false, ExitCode: 1},
	}
	section = evaluator.buildToolSection(tools)
	if !strings.Contains(section, "failed") {
		t.Errorf("expected failed status, got %q", section)
	}

	// Test truncation of long result
	longResult := strings.Repeat("y", 1000)
	tools = []ToolRecord{
		{Name: "Read", Result: longResult, Success: true},
	}
	section = evaluator.buildToolSection(tools)
	if !strings.Contains(section, "...[truncated]") {
		t.Errorf("expected truncation for long result, got %q", section[:200])
	}
}

func TestGoalEvaluatorBuildGoalContextSection(t *testing.T) {
	evaluator := NewAnthropicGoalEvaluator(GoalEvaluatorCfg{APIKey: "test"})

	ctx := GoalContext{
		Iteration: 3,
		MaxTurns:  10,
		Elapsed:   5 * time.Minute,
		WorkDir:   "/home/user/project",
	}
	section := evaluator.buildGoalContextSection(ctx)

	if !strings.Contains(section, "Iteration: 3 of 10") {
		t.Errorf("expected iteration info, got %q", section)
	}
	if !strings.Contains(section, "/home/user/project") {
		t.Errorf("expected workdir, got %q", section)
	}
	if !strings.Contains(section, "5m") {
		t.Errorf("expected elapsed time, got %q", section)
	}
}

func TestRoughTokenEstimate(t *testing.T) {
	// Test basic estimation
	s := "hello world" // 11 chars = ~2.75 tokens, rounds to 2
	estimate := roughTokenEstimate(s)
	if estimate < 2 || estimate > 3 {
		t.Errorf("expected ~2-3 tokens for 11 chars, got %d", estimate)
	}

	// Test unicode (Chinese chars count as runes)
	s = "你好世界" // 4 Chinese chars = 4 runes = ~1 token (each Chinese char is ~1 token)
	estimate = roughTokenEstimate(s)
	// Our simple estimate is chars/4, so 4/4 = 1
	if estimate != 1 {
		t.Errorf("expected 1 token for 4 Chinese chars, got %d", estimate)
	}

	// Test longer string
	s = strings.Repeat("a", 1000) // 1000 chars = 250 tokens
	estimate = roughTokenEstimate(s)
	if estimate != 250 {
		t.Errorf("expected 250 tokens for 1000 chars, got %d", estimate)
	}
}

// --- cmdGoal tests ---

func TestCmdGoal_Start(t *testing.T) {
	p := &stubGoalPlatform{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetGoalMaxTurns(10)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	// Start goal mode
	result := e.cmdGoal(p, msg, []string{"fix", "all", "tests"})

	// Should return false (passthrough to agent)
	if result {
		t.Errorf("expected passthrough (false), got true")
	}

	// Should send start message
	if len(p.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "Goal mode activated") {
		t.Errorf("expected goal start message, got %q", p.sent[0])
	}

	// Goal state should be initialized
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

	// Without active goal
	result := e.cmdGoalStatus(p, msg)
	if !result {
		t.Errorf("expected true (handled), got false")
	}
	if !strings.Contains(p.sent[len(p.sent)-1], "No goal") {
		t.Errorf("expected 'no goal' message, got %q", p.sent[len(p.sent)-1])
	}

	// Start goal
	e.initGoalState("test:user1", "fix the bug")

	// With active goal
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

	// Without active goal
	result := e.cmdGoalClear(p, msg)
	if !result {
		t.Errorf("expected true (handled), got false")
	}
	if !strings.Contains(p.sent[len(p.sent)-1], "No goal") {
		t.Errorf("expected 'no goal' message, got %q", p.sent[len(p.sent)-1])
	}

	// Start goal
	e.initGoalState("test:user1", "fix the bug")

	// With active goal
	result = e.cmdGoalClear(p, msg)
	if !result {
		t.Errorf("expected true (handled), got false")
	}
	if !strings.Contains(p.sent[len(p.sent)-1], "aborted") {
		t.Errorf("expected 'aborted' message, got %q", p.sent[len(p.sent)-1])
	}

	// Goal should be cleared
	state := e.getGoalState("test:user1")
	if state != nil {
		t.Errorf("expected nil goal state after clear, got %+v", state)
	}
}

func TestGoalState_Management(t *testing.T) {
	p := &stubGoalPlatform{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetGoalMaxTurns(5)

	// Test initialization
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

	// Test clear
	e.clearGoalState("test:user1")
	state = e.getGoalState("test:user1")
	if state != nil {
		t.Errorf("expected nil after clear, got %+v", state)
	}
}
