// Package review implements the `nlsh review` subcommand.
package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

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

Do not include preamble. Start directly with useful observations. Keep it under 500 words.`

// Run executes the review agent and optionally writes the result.
func Run(cfg types.Config, dryRun bool, show bool, clear bool) error {
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

	// Load full history for review.
	entries, err := history.Load(cfg.History.File)
	if err != nil {
		return fmt.Errorf("could not load history: %w", err)
	}
	if len(entries) == 0 {
		fmt.Println("No history to review.")
		return nil
	}

	historyJSON, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}

	client, err := provider.New(cfg.Provider)
	if err != nil {
		return err
	}

	userMsg := fmt.Sprintf("Here is my full nlsh history as of %s. Please analyze it and write the review prompt.\n\n```json\n%s\n```",
		time.Now().Format("2006-01-02"),
		string(historyJSON),
	)

	ctx := context.Background()
	result, err := client.Complete(ctx, reviewSystemPrompt, []provider.Message{
		{Role: "user", Content: userMsg},
	})
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Println(result)
		return nil
	}

	if err := os.WriteFile(reviewFile, []byte(result+"\n"), 0644); err != nil {
		return fmt.Errorf("could not write review file: %w", err)
	}
	fmt.Printf("Review written to %s\n", reviewFile)
	return nil
}
