// Package harness history manages the harness-specific history file.
package harness

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/Merith-TK/nlsh/internal/types"
)

// LoadHarnessHistory reads all harness session entries.
func LoadHarnessHistory(path string) ([]types.SessionHistory, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var entries []types.SessionHistory
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// writeHarnessHistory appends a session entry to the harness history file.
func writeHarnessHistory(path string, entry types.SessionHistory) error {
	entries, err := LoadHarnessHistory(path)
	if err != nil {
		return err
	}
	entries = append(entries, entry)
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// recallHistory searches harness history for matching entries.
func recallHistory(path, query string, limit int) string {
	entries, err := LoadHarnessHistory(path)
	if err != nil || len(entries) == 0 {
		return "No prior harness history found."
	}

	if limit <= 0 {
		limit = 5
	}

	queryLower := strings.ToLower(query)
	var matches []string
	count := 0

	// Search from newest to oldest.
	for i := len(entries) - 1; i >= 0 && count < limit; i-- {
		entry := entries[i]
		for _, turn := range entry.Turns {
			if strings.Contains(strings.ToLower(turn.Prompt), queryLower) ||
				strings.Contains(strings.ToLower(strings.Join(turn.Commands, " ")), queryLower) {
				match := formatTurn(turn)
				matches = append(matches, match)
				count++
				if count >= limit {
					break
				}
			}
		}
	}

	if len(matches) == 0 {
		return "No matching history entries found."
	}

	return "Recalled entries:\n" + strings.Join(matches, "\n")
}

func formatTurn(turn types.SessionTurn) string {
	var sb strings.Builder
	sb.WriteString("- Prompt: " + turn.Prompt + "\n")
	sb.WriteString("  Commands: " + strings.Join(turn.Commands, " && ") + "\n")
	sb.WriteString("  Risk: " + turn.Risk + " | Outcome: " + turn.Outcome + "\n")
	return sb.String()
}
