package core

import (
	"regexp"
	"strings"
)

var (
	reCodeBlock       = regexp.MustCompile("(?s)```[a-zA-Z]*\n?(.*?)```")
	reInlineCode      = regexp.MustCompile("`([^`]+)`")
	reBoldAst         = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reBoldUnd         = regexp.MustCompile(`__(.+?)__`)
	reItalicAst       = regexp.MustCompile(`\*(.+?)\*`)
	reItalicUnd       = regexp.MustCompile(`_(.+?)_`)
	reStrike          = regexp.MustCompile(`~~(.+?)~~`)
	reLink            = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reHeading         = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	reHorizontal      = regexp.MustCompile(`(?m)^[-*_]{3,}\s*$`)
	reBlockquote      = regexp.MustCompile(`(?m)^>\s?`)
	reMultipleNewline = regexp.MustCompile(`\n{3,}`)
)

// StripMarkdown converts Markdown-formatted text to clean plain text.
// Useful for platforms that don't support Markdown rendering (WeChat, LINE, etc.).
// Code blocks are indented with "│ " prefix for visual distinction.
func StripMarkdown(s string) string {
	// Code blocks: add visual boundary with indent prefix
	s = reCodeBlock.ReplaceAllStringFunc(s, func(m string) string {
		// Extract the code content (group 1)
		matches := reCodeBlock.FindStringSubmatch(m)
		if len(matches) < 2 {
			return m
		}
		code := matches[1]
		code = strings.TrimSpace(code)
		if code == "" {
			return ""
		}
		// Add indent to each line for visual distinction
		lines := strings.Split(code, "\n")
		var out strings.Builder
		for _, line := range lines {
			out.WriteString("│ ")
			out.WriteString(line)
			out.WriteString("\n")
		}
		return "\n" + strings.TrimSuffix(out.String(), "\n") + "\n"
	})

	// Inline code — keep content
	s = reInlineCode.ReplaceAllString(s, "$1")

	// Bold / italic / strikethrough — keep text
	s = reBoldAst.ReplaceAllString(s, "$1")
	s = reBoldUnd.ReplaceAllString(s, "$1")
	s = reItalicAst.ReplaceAllString(s, "$1")
	s = reItalicUnd.ReplaceAllString(s, "$1")
	s = reStrike.ReplaceAllString(s, "$1")

	// Links [text](url) → text (url)
	s = reLink.ReplaceAllString(s, "$1 ($2)")

	// Headings — remove # prefix
	s = reHeading.ReplaceAllString(s, "")

	// Horizontal rules (---, ***, ___) — remove entirely
	s = reHorizontal.ReplaceAllString(s, "")

	// Blockquotes
	s = reBlockquote.ReplaceAllString(s, "")

	// Collapse multiple consecutive blank lines into single blank line
	s = reMultipleNewline.ReplaceAllString(s, "\n\n")

	return strings.TrimSpace(s)
}