// Package run contains the core nlsh run loop.
package run

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Merith-TK/nlsh/internal/executor"
	"github.com/Merith-TK/nlsh/internal/history"
	"github.com/Merith-TK/nlsh/internal/prompt"
	"github.com/Merith-TK/nlsh/internal/provider"
	"github.com/Merith-TK/nlsh/internal/types"
)

// Execute performs the full nlsh flow for a natural language input.
func Execute(cfg types.Config, opts types.RunOptions) error {
	ctx := context.Background()

	// Build provider config, respecting CLI overrides.
	provCfg := cfg.Provider
	if opts.Provider != "" {
		provCfg.Type = opts.Provider
	}
	if opts.Model != "" {
		provCfg.Model = opts.Model
	}

	client, err := provider.New(provCfg)
	if err != nil {
		return err
	}

	// Load history for context.
	var histEntries []types.HistoryEntry
	if !opts.NoHistory {
		histEntries, err = history.Last(cfg.History.File, cfg.History.ContextEntries)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not load history: %v\n", err)
		}
	}

	// Assemble system prompt.
	home, _ := os.UserHomeDir()
	reviewFile := home + "/nlsh_review.md"
	systemPrompt := prompt.Assemble(reviewFile, cfg.Prompts.MasterPromptFile, opts.Prompt, histEntries)

	// Build initial conversation.
	messages := []provider.Message{
		{Role: "user", Content: opts.Input},
	}

	// Main loop — handles inline refinement and rejection continuation.
	for {
		raw, err := callAgent(ctx, client, systemPrompt, messages)
		if err != nil {
			return err
		}

		resp, err := provider.ParseAgentResponse(raw)
		if err != nil {
			// Retry once.
			messages = append(messages, provider.Message{Role: "assistant", Content: raw})
			messages = append(messages, provider.Message{Role: "user", Content: "Your previous response was not valid JSON. Return only valid JSON matching the schema."})
			raw2, err2 := callAgent(ctx, client, systemPrompt, messages)
			if err2 != nil {
				return err2
			}
			resp, err = provider.ParseAgentResponse(raw2)
			if err != nil {
				return fmt.Errorf("agent returned invalid JSON twice:\n%s", raw2)
			}
			raw = raw2
		}

		// Apply risk overrides from config.
		resp.Risk = applyRiskOverrides(resp, cfg.Risk)

		// Display translated commands.
		printTranslation(resp, opts.Plain)

		if opts.DryRun {
			return nil
		}

		autoApprove := opts.AutoApprove || cfg.Risk.AutoApprove

		if resp.Risk == "LOW" || autoApprove {
			// Auto-execute.
			outcome, execErr := executeCommands(resp)
			if !opts.NoHistory {
				writeHistory(cfg.History.File, opts.Input, resp, outcome, client)
			}
			return execErr
		}

		// HIGH risk — confirm prompt.
		answer, promptErr := confirmPrompt(opts.Plain)
		if promptErr != nil {
			return promptErr
		}

		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y":
			outcome, execErr := executeCommands(resp)
			if !opts.NoHistory {
				writeHistory(cfg.History.File, opts.Input, resp, outcome, client)
			}
			return execErr

		case "n":
			if !opts.NoHistory {
				writeHistory(cfg.History.File, opts.Input, resp, types.OutcomeRejected, client)
			}
			// Rejection continuation.
			clarification, quitErr := rejectionPrompt(opts.Plain)
			if quitErr != nil {
				return quitErr
			}
			// Build next turn: append the assistant response and the rejection context.
			messages = append(messages,
				provider.Message{Role: "assistant", Content: raw},
				provider.Message{Role: "user", Content: "Rejected. " + clarification},
			)
			continue

		case "q", "quit":
			if !opts.NoHistory {
				writeHistory(cfg.History.File, opts.Input, resp, types.OutcomeCanceled, client)
			}
			fmt.Println("Canceled.")
			os.Exit(1)

		default:
			// Natural language refinement.
			if !opts.NoHistory {
				writeHistory(cfg.History.File, opts.Input, resp, types.OutcomeRejected, client)
			}
			messages = append(messages,
				provider.Message{Role: "assistant", Content: raw},
				provider.Message{Role: "user", Content: answer},
			)
			continue
		}
	}
}

func callAgent(ctx context.Context, client provider.Client, systemPrompt string, messages []provider.Message) (string, error) {
	raw, err := client.Complete(ctx, systemPrompt, messages)
	if err != nil {
		return "", err
	}
	return raw, nil
}

func printTranslation(resp types.AgentResponse, plain bool) {
	fmt.Println()
	fmt.Println("  Translated:")
	if resp.Chained {
		fmt.Println("   ", strings.Join(resp.Commands, " && "))
	} else {
		for _, cmd := range resp.Commands {
			fmt.Println("   ", cmd)
		}
	}
	fmt.Println()
	fmt.Printf("  Risk: %s — %s\n", resp.Risk, resp.Rationale)
	fmt.Println()
}

func confirmPrompt(plain bool) (string, error) {
	fmt.Print("  Run? [Y/n/...]: ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "q", nil
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func rejectionPrompt(plain bool) (string, error) {
	fmt.Println()
	fmt.Println("  Rejected. Clarify or rephrase (or 'q' to cancel):")
	fmt.Print("  > ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("canceled")
	}
	line = strings.TrimRight(line, "\r\n")
	if strings.ToLower(line) == "q" || strings.ToLower(line) == "quit" {
		fmt.Println("Canceled.")
		os.Exit(1)
	}
	return line, nil
}

func executeCommands(resp types.AgentResponse) (string, error) {
	var err error
	if resp.Chained {
		err = executor.RunChained(resp.Commands)
	} else {
		err = executor.RunSequential(resp.Commands)
	}
	if err != nil {
		return types.OutcomeCanceled, err
	}
	return types.OutcomeApproved, nil
}

func writeHistory(path, input string, resp types.AgentResponse, outcome string, client provider.Client) {
	entry := types.HistoryEntry{
		Timestamp: time.Now().UTC(),
		Prompt:    input,
		Commands:  resp.Commands,
		Risk:      resp.Risk,
		Outcome:   outcome,
		Provider:  client.ProviderName(),
		Model:     client.ModelName(),
	}
	if err := history.Append(path, entry); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write history: %v\n", err)
	}
}

func applyRiskOverrides(resp types.AgentResponse, riskCfg types.RiskConfig) string {
	cmdLine := strings.Join(resp.Commands, " ")
	for _, pattern := range riskCfg.HighRiskOverrides {
		if strings.Contains(cmdLine, pattern) {
			return "HIGH"
		}
	}
	for _, pattern := range riskCfg.LowRiskOverrides {
		if strings.Contains(cmdLine, pattern) {
			return "LOW"
		}
	}
	return resp.Risk
}
