// Package history manages reading and writing nlsh_history.json.
package history

import (
	"encoding/json"
	"os"

	"github.com/Merith-TK/nlsh/internal/types"
)

// Load reads all history entries from the given file path.
// Returns an empty slice if the file doesn't exist.
func Load(path string) ([]types.HistoryEntry, error) {
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
	var entries []types.HistoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// Append adds a single entry to the history file, creating it if needed.
func Append(path string, entry types.HistoryEntry) error {
	entries, err := Load(path)
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

// Last returns the last n entries from the history file.
func Last(path string, n int) ([]types.HistoryEntry, error) {
	entries, err := Load(path)
	if err != nil {
		return nil, err
	}
	if len(entries) <= n {
		return entries, nil
	}
	return entries[len(entries)-n:], nil
}

// Clear deletes the history file.
func Clear(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
