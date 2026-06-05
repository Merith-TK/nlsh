// Package review implements the `nlsh review` subcommand.
package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Merith-TK/nlsh/internal/harness"
	"github.com/Merith-TK/nlsh/internal/history"
	"github.com/Merith-TK/nlsh/internal/provider"
	"github.com/Merith-TK/nlsh/internal/types"
)

const reviewSystemPrompt = `You are analyzing a user's nlsh (natural language shell) command history to produce a compact behavioral summary.

Your output will be prepended to every future agent context as "Layer 1 — Review Prompt". Write it in free-form markdown. Be direct, specific, and actionable. Focus on:
- Common directories, stack names, tools, and workflows
- Rejection patterns (what kinds of commands get rejected or refined)
- Prompt tendencies (how the user phrases things, implied context)
- Commands that always auto-approve vs. always trigger refinement
- Differences between one-shot and harness mode usage if apparent

Do not include preamble. Start directly with useful observations. Keep it under 500 words.`

// Run executes the review agent and optionally writes the result.
func Run(cfg types.Config, dryRun bool, show bool, clear bool, oneShotOnly bool, harnessOnly bool) error {
	home, _ := os.UserHomeDir()
	reviewFile := home + "/nlsh_review.md"

	if show {
		data, err := os.ReadFile(reviewFile)
		if os.IsNotExist(err) {
			fmt.Println("(no review prompt found)")
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	if clear {
		if err := os.Remove(reviewFile); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("Review prompt cleared.")
		return nil
	}

	// Gather histories.
	var oneShotEntries []types.HistoryEntry
	var harnessEntries []types.SessionHistory
	var err error

	if !harnessOnly {
		oneShotEntries, err = history.Load(cfg.History.File)
		if err != nil {
			return fmt.Errorf("could not load one-shot history: %w", err)
		}
	}
	if !oneShotOnly {
		harnessEntries, err = harness.LoadHarnessHistory(cfg.Harness.HistoryFile)
		if err != nil {
			return fmt.Errorf("could not load harness history: %w", err)
		}
	}

	if len(oneShotEntries) == 0 && len(harnessEntries) == 0 {
		fmt.Println("No history to review.")
		return nil
	}

	// Build combined analysis payload.
	var parts []string
	if len(oneShotEntries) > 0 {
		data, _ := json.MarshalIndent(oneShotEntries, "", "  ")
		parts = append(parts, fmt.Sprintf("## One-Shot History (%d entries)\n\n```json\n%s\n```", len(oneShotEntries), string(data)))
	}
	if len(harnessEntries) > 0 {
		data, _ := json.MarshalIndent(harnessEntries, "", "  ")
		parts = append(parts, fmt.Sprintf("## Harness History (%d sessions)\n\n```json\n%s\n```", len(harnessEntries), string(data)))
	}

	client, err := provider.New(cfg.Provider)
	if err != nil {
		return err
	}

	userMsg := fmt.Sprintf("Here is my nlsh history as of %s. Please analyze it and write the review prompt.\n\n%s",
		time.Now().Format("2006-01-02"),
		strings.Join(parts, "\n\n"),
	)

	ctx := context.Background()
	result, err := client.Complete(ctx, reviewSystemPrompt, []provider.Message{
		{Role: "user", Content: userMsg},
	}, nil)
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Println(result.Content)
		return nil
	}

	if err := os.WriteFile(reviewFile, []byte(result.Content+"\n"), 0644); err != nil {
		return fmt.Errorf("could not write review file: %w", err)
	}
	fmt.Printf("Review written to %s\n", reviewFile)
	return nil
}
