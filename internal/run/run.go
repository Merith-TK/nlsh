// Package run contains the core nlsh run loop.
package run

import (
	"bufio"
	"context"
	"encoding/json"
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

// presentCommandTool is the tool the agent uses to return translated commands.
var presentCommandTool = provider.Tool{
	Name:        "present_command",
	Description: "Present the translated shell command(s) to the user with a risk assessment.",
	Schema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"commands": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Ordered list of shell commands to run",
			},
			"chained": map[string]any{
				"type":        "boolean",
				"description": "If true, join with && and run as one shell invocation",
			},
			"risk": map[string]any{
				"type":        "string",
				"enum":        []string{"LOW", "HIGH"},
				"description": "Risk level of the command set",
			},
			"rationale": map[string]any{
				"type":        "string",
				"description": "One sentence explaining the risk assessment",
			},
		},
		"required": []string{"commands", "chained", "risk", "rationale"},
	},
}

// Execute performs the full nlsh flow for a natural language input.
func Execute(cfg types.Config, opts types.RunOptions) error {
	ctx := context.Background()

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

	if provCfg.FallbackTools {
		fmt.Fprintln(os.Stderr, "warning: provider does not support tool-calling; using fallback mode")
	}

	var histEntries []types.HistoryEntry
	if !opts.NoHistory {
		histEntries, err = history.Last(cfg.History.File, cfg.History.ContextEntries)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not load history: %v\n", err)
		}
	}

	home, _ := os.UserHomeDir()
	reviewFile := home + "/nlsh_review.md"
	systemPrompt := prompt.Assemble(reviewFile, cfg.Prompts.MasterPromptFile, opts.Prompt, histEntries, provCfg.FallbackTools)

	messages := []provider.Message{
		{Role: "user", Content: opts.Input},
	}

	return runLoop(ctx, cfg, opts, client, systemPrompt, messages, opts.Input)
}

// runLoop handles translation, confirmation, execution, and refinement.
func runLoop(ctx context.Context, cfg types.Config, opts types.RunOptions, client provider.Client, systemPrompt string, messages []provider.Message, originalInput string) error {
	for {
		resp, err := translate(ctx, client, systemPrompt, messages)
		if err != nil {
			return err
		}

		resp.Risk = applyRiskOverrides(resp, cfg.Risk)
		printTranslation(resp, opts.Plain)

		if opts.DryRun {
			return nil
		}

		autoApprove := opts.AutoApprove || cfg.Risk.AutoApprove

		if resp.Risk == "LOW" || autoApprove {
			outcome, execErr := executeCommands(resp)
			if !opts.NoHistory {
				writeHistory(cfg.History.File, originalInput, resp, outcome, client)
			}
			return execErr
		}

		answer, promptErr := confirmPrompt(opts.Plain)
		if promptErr != nil {
			return promptErr
		}

		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y":
			outcome, execErr := executeCommands(resp)
			if !opts.NoHistory {
				writeHistory(cfg.History.File, originalInput, resp, outcome, client)
			}
			return execErr

		case "n":
			if !opts.NoHistory {
				writeHistory(cfg.History.File, originalInput, resp, types.OutcomeRejected, client)
			}
			clarification, quitErr := rejectionPrompt(opts.Plain)
			if quitErr != nil {
				return quitErr
			}
			messages = append(messages,
				provider.Message{Role: "assistant", Content: marshalAgentResponse(resp)},
				provider.Message{Role: "user", Content: "Rejected. " + clarification},
			)
			continue

		case "q", "quit":
			if !opts.NoHistory {
				writeHistory(cfg.History.File, originalInput, resp, types.OutcomeCanceled, client)
			}
			fmt.Println("Canceled.")
			os.Exit(1)

		default:
			if !opts.NoHistory {
				writeHistory(cfg.History.File, originalInput, resp, types.OutcomeRejected, client)
			}
			messages = append(messages,
				provider.Message{Role: "assistant", Content: marshalAgentResponse(resp)},
				provider.Message{Role: "user", Content: answer},
			)
			continue
		}
	}
}

// translate calls the agent and extracts the AgentResponse.
func translate(ctx context.Context, client provider.Client, systemPrompt string, messages []provider.Message) (types.AgentResponse, error) {
	result, err := client.Complete(ctx, systemPrompt, messages, []provider.Tool{presentCommandTool})
	if err != nil {
		return types.AgentResponse{}, err
	}

	if result.ToolCall != nil && result.ToolCall.Name == "present_command" {
		var resp types.AgentResponse
		argsJSON, _ := json.Marshal(result.ToolCall.Arguments)
		if err := json.Unmarshal(argsJSON, &resp); err != nil {
			return resp, fmt.Errorf("failed to parse present_command arguments: %w", err)
		}
		return resp, nil
	}

	// Fallback mode: parse content as raw JSON.
	return provider.ParseAgentResponse(result.Content)
}

func marshalAgentResponse(resp types.AgentResponse) string {
	b, _ := json.Marshal(resp)
	return string(b)
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
