package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// GoalEvaluator evaluates whether a goal condition has been met.
type GoalEvaluator interface {
	// Evaluate checks if the goal condition is satisfied.
	// When sessionID is provided, the evaluator can use --resume to access full context.
	Evaluate(ctx context.Context, sessionID string, condition string, goalCtx GoalContext) (GoalEvalResult, error)
}

// GoalEvalResult describes the outcome of a goal evaluation.
type GoalEvalResult struct {
	// Met is true if the goal condition is satisfied
	Met bool
	// Impossible is true if the goal can never be satisfied
	Impossible bool
	// Reason explains the evaluation decision
	Reason string
}

// GoalContext provides additional context for goal evaluation.
type GoalContext struct {
	Iteration int           // current iteration number (1-based)
	MaxTurns  int           // maximum iterations allowed
	Elapsed   time.Duration // time since goal mode started
	WorkDir   string        // current working directory
}

// CLIGoalEvaluator implements GoalEvaluator by invoking the agent CLI.
// When sessionID is provided, it uses --fork-session --resume to inherit
// the session history without conflicting with the active session.
type CLIGoalEvaluator struct {
	CLIBin  string // CLI binary name or path (e.g., "claude")
	WorkDir string // working directory for CLI invocation
	Timeout time.Duration
}

// NewCLIGoalEvaluator creates a new evaluator using CLI invocation.
func NewCLIGoalEvaluator(cliBin, workDir string) *CLIGoalEvaluator {
	return &CLIGoalEvaluator{
		CLIBin:  cliBin,
		WorkDir: workDir,
		Timeout: 60 * time.Second,
	}
}

const goalEvaluatorPrompt = `You are evaluating whether a goal condition is satisfied.

Review the conversation history above (from --resume) and determine if the goal has been achieved.

Your response must be a JSON object with this structure:
{
  "continue": true/false,
  "reason": "<explanation>",
  "impossible": true  // optional, only when goal can never be satisfied
}

Rules:
1. "continue": false means the goal is complete. "continue": true means more work is needed.
2. "reason" must explain what evidence you found or what is still missing.
3. Pay close attention to tool results in the history — they contain key evidence (test pass/fail, file creation, command output).
4. If the conversation shows the goal condition is satisfied, return {"continue": false, "reason": "evidence from conversation"}.
5. If the goal seems impossible, return {"continue": true, "impossible": true, "reason": "why impossible"}.
6. Be strict: when in doubt, return "continue": true with reason explaining what's missing.`

func (e *CLIGoalEvaluator) Evaluate(ctx context.Context, sessionID string, condition string, goalCtx GoalContext) (GoalEvalResult, error) {
	evalCtx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()

	// Build the evaluation prompt
	prompt := fmt.Sprintf(`%s

**Goal condition**: %s
**Iteration**: %d of %d
**Time elapsed**: %s

Evaluate whether the goal condition is satisfied based on the conversation history. Return JSON only.`,
		goalEvaluatorPrompt, condition, goalCtx.Iteration, goalCtx.MaxTurns, goalCtx.Elapsed.Round(time.Second))

	// Build CLI args
	args := []string{"-p", prompt, "--output-format", "json"}

	// Use --fork-session --resume to inherit history without conflicting
	if sessionID != "" {
		args = []string{
			"--fork-session",
			"--resume", sessionID,
			"-p", prompt,
			"--output-format", "json",
		}
	}

	cmd := exec.CommandContext(evalCtx, e.CLIBin, args...)
	if e.WorkDir != "" {
		cmd.Dir = e.WorkDir
	}

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return GoalEvalResult{}, fmt.Errorf("goal evaluator CLI exited %d: %s", exitErr.ExitCode(), string(exitErr.Stderr))
		}
		return GoalEvalResult{}, fmt.Errorf("goal evaluator CLI: %w", err)
	}

	text := extractCLIResultText(string(out))

	evalResult, err := parseEvalResult(text)
	if err != nil {
		slog.Warn("goal: failed to parse evaluator response", "error", err, "response", text)
		return GoalEvalResult{
			Met:    false,
			Reason: "could not parse evaluator response",
		}, nil
	}

	return evalResult, nil
}

// extractCLIResultText extracts the assistant's text from CLI JSON output.
func extractCLIResultText(raw string) string {
	raw = strings.TrimSpace(raw)

	// Try as a single JSON object with "result" field
	var single struct {
		Result string `json:"result"`
	}
	if json.Unmarshal([]byte(raw), &single) == nil && single.Result != "" {
		return single.Result
	}

	// Try as array of JSON objects, pick last with type "result"
	var arr []map[string]any
	if json.Unmarshal([]byte(raw), &arr) == nil {
		for i := len(arr) - 1; i >= 0; i-- {
			item := arr[i]
			if t, _ := item["type"].(string); t == "result" {
				if result, _ := item["result"].(string); result != "" {
					return result
				}
			}
		}
		// Fallback: look for assistant messages with content
		for i := len(arr) - 1; i >= 0; i-- {
			item := arr[i]
			if t, _ := item["type"].(string); t == "assistant" {
				if content, _ := item["content"].(string); content != "" {
					return content
				}
				if contentArr, ok := item["content"].([]any); ok {
					for _, c := range contentArr {
						if block, ok := c.(map[string]any); ok {
							if text, _ := block["text"].(string); text != "" {
								return text
							}
						}
					}
				}
			}
		}
	}

	return raw
}

// parseEvalResult parses the JSON evaluation result.
func parseEvalResult(text string) (GoalEvalResult, error) {
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
		Continue   bool   `json:"continue"`
		Reason     string `json:"reason"`
		Impossible bool   `json:"impossible"`
	}
	if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
		return GoalEvalResult{}, err
	}

	return GoalEvalResult{
		Met:        !parsed.Continue,
		Impossible: parsed.Impossible,
		Reason:     parsed.Reason,
	}, nil
}