// Package prompt assembles the layered system prompt for the agent.
package prompt

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Merith-TK/nlsh/internal/types"
)

const kissSystemPrompt = `You are a shell command translator. Given a natural language description of what the user wants to do, translate it into the minimum viable shell command(s).

Rules:
- Prefer simple, readable commands over clever one-liners.
- Never hallucinate flags or paths not clearly implied by the input or history.
- Assign risk honestly: LOW for read-only/safe operations, HIGH for anything destructive, stateful, or irreversible.
- Return ONLY valid JSON matching this exact schema — no explanation, no markdown, no extra text:
  {
    "commands": ["<cmd1>", "<cmd2>"],
    "chained": true|false,
    "risk": "LOW"|"HIGH",
    "rationale": "<one sentence>"
  }
- "chained": true means join commands with && in a single shell invocation. false means run sequentially with output shown between each.`

// Assemble builds the full system prompt string from all layers.
func Assemble(
	reviewFile string,
	masterPromptFile string,
	extraPrompt string,
	historyEntries []types.HistoryEntry,
) string {
	var parts []string

	// Layer 1: Review prompt
	if content, err := readFile(reviewFile); err == nil && content != "" {
		parts = append(parts, "## User Review\n\n"+content)
	}

	// Layer 2: Master prompt + optional inline addition
	master, _ := readFile(masterPromptFile)
	if extraPrompt != "" {
		if master != "" {
			master = master + "\n\n" + extraPrompt
		} else {
			master = extraPrompt
		}
	}
	if master != "" {
		parts = append(parts, "## User Instructions\n\n"+master)
	}

	// Layer 3: KISS system prompt
	parts = append(parts, kissSystemPrompt)

	// Layer 4: History block
	if len(historyEntries) > 0 {
		parts = append(parts, "## Recent History\n\n"+formatHistory(historyEntries))
	}

	return strings.Join(parts, "\n\n---\n\n")
}

func readFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func formatHistory(entries []types.HistoryEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("- [%s] prompt: %q | commands: %s | risk: %s | outcome: %s\n",
			e.Timestamp.Format("2006-01-02T15:04:05Z"),
			e.Prompt,
			formatCommands(e.Commands),
			e.Risk,
			e.Outcome,
		))
	}
	return sb.String()
}

func formatCommands(cmds []string) string {
	data, _ := json.Marshal(cmds)
	return string(data)
}
