package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// GoalEvaluator evaluates whether a goal condition has been met.
type GoalEvaluator interface {
	// Evaluate checks if the goal condition is satisfied based on conversation history,
	// tool records from the current turn, and additional goal context.
	// Returns the evaluation result or an error.
	Evaluate(ctx context.Context, condition string, history []HistoryEntry, tools []ToolRecord, goalCtx GoalContext) (GoalEvalResult, error)
}

// GoalEvalResult describes the outcome of a goal evaluation.
type GoalEvalResult struct {
	// Met is true if the goal condition is satisfied
	Met bool
	// Impossible is true if the goal can never be satisfied
	Impossible bool
	// Reason explains the evaluation decision
	Reason string
	// Tokens used by the evaluator call
	InputTokens  int
	OutputTokens int
}

// ToolRecord represents a tool invocation and its result.
type ToolRecord struct {
	Name      string // tool name (e.g., "Bash", "Write", "Read")
	Input     string // human-readable input summary
	Result    string // tool result output
	Status    string // status (e.g., "completed", "failed")
	Success   bool   // whether the tool succeeded
	ExitCode  int    // exit code if applicable
}

// GoalContext provides additional context for goal evaluation.
type GoalContext struct {
	Iteration    int       // current iteration number (1-based)
	MaxTurns    int       // maximum iterations allowed
	Elapsed     time.Duration // time since goal mode started
	WorkDir     string    // current working directory
}

// GoalEvaluatorCfg holds configuration for the goal evaluator.
type GoalEvaluatorCfg struct {
	Enabled   bool
	APIKey    string
	BaseURL   string
	Model     string // defaults to "claude-sonnet-4-20250514"
	MaxTokens int    // max tokens for evaluator response
}

// AnthropicGoalEvaluator implements GoalEvaluator using Anthropic's Messages API.
type AnthropicGoalEvaluator struct {
	APIKey      string
	BaseURL     string
	Model       string
	MaxTokens   int
	Client      *http.Client
	TokenBudget int // max input tokens for evaluation context (default 16000)
}

// NewAnthropicGoalEvaluator creates a new evaluator using Anthropic API.
func NewAnthropicGoalEvaluator(cfg GoalEvaluatorCfg) *AnthropicGoalEvaluator {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "claude-sonnet-4-20250514"
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 256
	}
	return &AnthropicGoalEvaluator{
		APIKey:      cfg.APIKey,
		BaseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		Model:       cfg.Model,
		MaxTokens:   cfg.MaxTokens,
		Client:      &http.Client{Timeout: 30 * time.Second},
		TokenBudget: 16000, // default 16k tokens for context
	}
}

// roughTokenEstimate provides a rough token estimate (~4 chars per token for mixed content)
func roughTokenEstimate(s string) int {
	return len([]rune(s)) / 4
}

const goalEvaluatorPrompt = `You are evaluating a goal condition in Claude Code. Read the conversation transcript and tool execution results carefully, then judge whether the user-provided condition is satisfied.

Your response must be a JSON object with one of these shapes:
- {"ok": true, "reason": "<quote evidence from the transcript or tool results that satisfies the condition>"}
- {"ok": false, "reason": "<quote what is missing or what blocks the condition>"}
- {"ok": false, "impossible": true, "reason": "<explain why the condition can never be satisfied>"}

Rules:
1. Always include a "reason" field, quoting specific text from the transcript or tool results whenever possible
2. Pay close attention to tool results — they often contain the key evidence for whether a goal is achieved (e.g., test pass/fail, file creation, command output)
3. If the transcript does not contain clear evidence that the condition is satisfied, return {"ok": false, "reason": "insufficient evidence in transcript"}
4. Only use {"ok": false, "impossible": true} when the condition is genuinely unachievable — for example: the condition is self-contradictory, it depends on a resource or capability that is unavailable, or the assistant has explicitly tried, exhausted reasonable approaches, and stated it cannot be done
5. Do not mark conditions impossible just because progress is slow or the goal isn't yet reached
6. Be strict: prefer {"ok": false} when in doubt`

func (e *AnthropicGoalEvaluator) Evaluate(ctx context.Context, condition string, history []HistoryEntry, tools []ToolRecord, goalCtx GoalContext) (GoalEvalResult, error) {
	// Build conversation transcript
	transcript := e.buildTranscript(history)

	// Build tool results section
	toolSection := e.buildToolSection(tools)

	// Build goal context section
	contextSection := e.buildGoalContextSection(goalCtx)

	prompt := fmt.Sprintf(`%s

Goal condition: %s
%s

Conversation transcript:
%s
%s

Evaluate whether the goal condition is satisfied. Return JSON only.`, goalEvaluatorPrompt, condition, contextSection, transcript, toolSection)

	reqBody := map[string]any{
		"model":      e.Model,
		"max_tokens": e.MaxTokens,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return GoalEvalResult{}, fmt.Errorf("marshal request: %w", err)
	}

	url := e.BaseURL + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		return GoalEvalResult{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", e.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := e.Client.Do(req)
	if err != nil {
		return GoalEvalResult{}, fmt.Errorf("evaluator request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return GoalEvalResult{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return GoalEvalResult{}, fmt.Errorf("evaluator API %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return GoalEvalResult{}, fmt.Errorf("parse response: %w", err)
	}

	if len(result.Content) == 0 {
		return GoalEvalResult{}, fmt.Errorf("evaluator: empty response")
	}

	evalResult, err := e.parseEvalResult(result.Content[0].Text)
	if err != nil {
		slog.Warn("goal: failed to parse evaluator response", "error", err, "response", result.Content[0].Text)
		// Default to continue on parse failure
		return GoalEvalResult{
			Met:          false,
			Reason:       "could not parse evaluator response",
			InputTokens:  result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens,
		}, nil
	}

	evalResult.InputTokens = result.Usage.InputTokens
	evalResult.OutputTokens = result.Usage.OutputTokens
	return evalResult, nil
}

func (e *AnthropicGoalEvaluator) buildTranscript(history []HistoryEntry) string {
	if len(history) == 0 {
		return ""
	}

	// Reserve tokens for prompt template (~500), goal condition (~100), context section (~100)
	// Remaining budget for history
	historyBudget := e.TokenBudget - 700
	if historyBudget < 1000 {
		historyBudget = 1000 // minimum 1k tokens for history
	}

	var buf strings.Builder
	tokensUsed := 0

	// Iterate from most recent to oldest, adding entries until budget exhausted
	for i := len(history) - 1; i >= 0; i-- {
		h := history[i]
		role := strings.ToUpper(h.Role)

		// Truncate single entry if too long (max 1500 chars ~375 tokens per entry)
		content := h.Content
		maxEntryChars := 1500
		if len(content) > maxEntryChars {
			content = content[:maxEntryChars] + "...[truncated]"
		}

		entry := fmt.Sprintf("[%s]: %s\n\n", role, content)
		entryTokens := roughTokenEstimate(entry)

		if tokensUsed + entryTokens > historyBudget {
			// Budget exhausted, note how many older entries were skipped
			if i > 0 {
				buf.WriteString(fmt.Sprintf("\n... [skipped %d older messages due to context limit]\n", i))
			}
			break
		}

		// Insert at beginning to maintain chronological order
		buf.WriteString(entry)
		tokensUsed += entryTokens
	}

	return buf.String()
}

func (e *AnthropicGoalEvaluator) buildToolSection(tools []ToolRecord) string {
	if len(tools) == 0 {
		return ""
	}

	// Reserve ~2000 tokens for tools (roughly 2 large tool outputs or many small ones)
	toolBudget := 2000
	if toolBudget > e.TokenBudget/4 {
		toolBudget = e.TokenBudget / 4
	}

	var buf strings.Builder
	buf.WriteString("\nTool execution results (current turn):\n")
	baseTokens := roughTokenEstimate(buf.String())
	tokensUsed := baseTokens

	for i, t := range tools {
		// Truncate input and result based on remaining budget per tool
		remainingBudget := (toolBudget - tokensUsed) / (len(tools) - i)
		maxResultChars := remainingBudget * 4 // ~4 chars per token
		if maxResultChars > 800 {
			maxResultChars = 800
		}
		if maxResultChars < 100 {
			maxResultChars = 100
		}

		result := t.Result
		if len(result) > maxResultChars {
			result = result[:maxResultChars] + "...[truncated]"
		}
		input := t.Input
		if len(input) > 200 {
			input = input[:200] + "...[truncated]"
		}
		status := "success"
		if !t.Success {
			status = "failed"
			if t.ExitCode != 0 {
				status = fmt.Sprintf("failed (exit %d)", t.ExitCode)
			}
		}
		if t.Status != "" {
			status = t.Status
		}

		entry := fmt.Sprintf("%d. Tool: %s | Status: %s\n   Input: %s\n   Result: %s\n\n",
			i+1, t.Name, status, input, result)
		tokensUsed += roughTokenEstimate(entry)
		buf.WriteString(entry)
	}

	return buf.String()
}

func (e *AnthropicGoalEvaluator) buildGoalContextSection(ctx GoalContext) string {
	var buf strings.Builder
	buf.WriteString("\nGoal context:\n")
	if ctx.WorkDir != "" {
		buf.WriteString(fmt.Sprintf("- Working directory: %s\n", ctx.WorkDir))
	}
	if ctx.MaxTurns > 0 {
		buf.WriteString(fmt.Sprintf("- Iteration: %d of %d\n", ctx.Iteration, ctx.MaxTurns))
	} else if ctx.Iteration > 0 {
		buf.WriteString(fmt.Sprintf("- Iteration: %d\n", ctx.Iteration))
	}
	if ctx.Elapsed > 0 {
		buf.WriteString(fmt.Sprintf("- Time elapsed: %s\n", ctx.Elapsed.Round(time.Second)))
	}
	return buf.String()
}

func (e *AnthropicGoalEvaluator) parseEvalResult(text string) (GoalEvalResult, error) {
	// Extract JSON from response (handle markdown code blocks)
	jsonText := text
	if strings.Contains(text, "```json") {
		start := strings.Index(text, "```json") + 7
		end := strings.Index(text[start:], "```")
		if end > 0 {
			jsonText = strings.TrimSpace(text[start : start+end])
		}
	} else if strings.Contains(text, "```") {
		start := strings.Index(text, "```") + 3
		end := strings.Index(text[start:], "```")
		if end > 0 {
			jsonText = strings.TrimSpace(text[start : start+end])
		}
	}

	var parsed struct {
		Ok         bool   `json:"ok"`
		Impossible bool   `json:"impossible"`
		Reason     string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
		return GoalEvalResult{}, err
	}

	return GoalEvalResult{
		Met:         parsed.Ok,
		Impossible:  parsed.Impossible,
		Reason:      parsed.Reason,
	}, nil
}
