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
	"github.com/Merith-TK/nlsh/internal/fs"
	"github.com/Merith-TK/nlsh/internal/history"
	"github.com/Merith-TK/nlsh/internal/prompt"
	"github.com/Merith-TK/nlsh/internal/provider"
	"github.com/Merith-TK/nlsh/internal/types"
)

// Tool definitions for the agent.
var (
	presentCommandTool = provider.Tool{
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

	exploreDirectoryTool = provider.Tool{
		Name:        "explore_directory",
		Description: "List the contents of a directory. Limited depth. Use only when needed to understand the workspace.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Directory path to explore (relative or absolute)",
				},
			},
			"required": []string{"path"},
		},
	}

	readFileTool = provider.Tool{
		Name:        "read_file",
		Description: "Read a text file with line limits. Use only when needed to understand file contents before translating.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path to read (relative or absolute)",
				},
			},
			"required": []string{"path"},
		},
	}
)

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
		resp, explored, err := translateWithTools(ctx, client, systemPrompt, messages, cfg.Provider.FallbackTools, 100, 1)
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
				comment := ""
				if explored {
					comment = "Agent explored workspace before translating"
				}
				writeHistory(cfg.History.File, originalInput, resp, outcome, client, comment)
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
				comment := ""
				if explored {
					comment = "Agent explored workspace before translating"
				}
				writeHistory(cfg.History.File, originalInput, resp, outcome, client, comment)
			}
			return execErr

		case "n":
			if !opts.NoHistory {
				writeHistory(cfg.History.File, originalInput, resp, types.OutcomeRejected, client, "")
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
				writeHistory(cfg.History.File, originalInput, resp, types.OutcomeCanceled, client, "")
			}
			fmt.Println("Canceled.")
			os.Exit(1)

		default:
			if !opts.NoHistory {
				writeHistory(cfg.History.File, originalInput, resp, types.OutcomeRejected, client, "")
			}
			messages = append(messages,
				provider.Message{Role: "assistant", Content: marshalAgentResponse(resp)},
				provider.Message{Role: "user", Content: answer},
			)
			continue
		}
	}
}

// translateWithTools calls the agent in a loop, executing filesystem tools until it presents a command.
// Returns (AgentResponse, exploredFlag, error).
func translateWithTools(ctx context.Context, client provider.Client, systemPrompt string, messages []provider.Message, fallback bool, maxLines int, maxDepth int) (types.AgentResponse, bool, error) {
	explored := false
	tools := []provider.Tool{presentCommandTool, exploreDirectoryTool, readFileTool}
	if fallback {
		tools = nil
	}

	for {
		result, err := client.Complete(ctx, systemPrompt, messages, tools)
		if err != nil {
			return types.AgentResponse{}, false, err
		}

		// Fallback mode: no tools, parse raw JSON.
		if fallback {
			resp, err := provider.ParseAgentResponse(result.Content)
			return resp, explored, err
		}

		if result.ToolCall == nil {
			return types.AgentResponse{}, explored, fmt.Errorf("agent did not call a tool; raw: %s", result.Content)
		}

		tc := result.ToolCall
		toolCallID := tc.ID
		if toolCallID == "" {
			toolCallID = fmt.Sprintf("%s_1", tc.Name)
		}

		switch tc.Name {
		case "present_command":
			var resp types.AgentResponse
			argsJSON, _ := json.Marshal(tc.Arguments)
			if err := json.Unmarshal(argsJSON, &resp); err != nil {
				return resp, explored, fmt.Errorf("failed to parse present_command arguments: %w", err)
			}
			return resp, explored, nil

		case "explore_directory":
			explored = true
			path, _ := tc.Arguments["path"].(string)
			res := fs.ExploreDirectory(path, maxDepth)
			content := res.Content
			if res.Error != "" {
				content = fmt.Sprintf("Error: %s", res.Error)
			}
			messages = append(messages,
				provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{*tc}},
				provider.Message{Role: "user", ToolResults: []provider.ToolResult{
					{ToolCallID: toolCallID, Name: "explore_directory", Content: content},
				}},
			)
			continue

		case "read_file":
			explored = true
			path, _ := tc.Arguments["path"].(string)
			res := fs.ReadFile(path, maxLines, false)
			content := res.Content
			if res.Error != "" {
				// Check if it's a secret ask prompt.
				if strings.Contains(res.Error, "Allow reading it for this session") {
					fmt.Println()
					fmt.Println("  ", res.Error)
					if fs.AskSecretPrompt(path) {
						// Re-read with allowSecret=true.
						res = fs.ReadFile(path, maxLines, true)
						content = res.Content
						if res.Error != "" {
							content = fmt.Sprintf("Error: %s", res.Error)
						}
					} else {
						content = "User declined to allow reading this file."
					}
				} else {
					content = fmt.Sprintf("Error: %s", res.Error)
				}
			}
			messages = append(messages,
				provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{*tc}},
				provider.Message{Role: "user", ToolResults: []provider.ToolResult{
					{ToolCallID: toolCallID, Name: "read_file", Content: content},
				}},
			)
			continue

		default:
			return types.AgentResponse{}, explored, fmt.Errorf("agent called unknown tool %q", tc.Name)
		}
	}
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

func writeHistory(path, input string, resp types.AgentResponse, outcome string, client provider.Client, comment string) {
	entry := types.HistoryEntry{
		Timestamp: time.Now().UTC(),
		Prompt:    input,
		Commands:  resp.Commands,
		Risk:      resp.Risk,
		Outcome:   outcome,
		Provider:  client.ProviderName(),
		Model:     client.ModelName(),
	}
	if comment != "" {
		// Store comment as a single-element array note in the commands field for visibility.
		// Actually, HistoryEntry doesn't have a comment field. Let's not add one to avoid schema changes.
		// Instead, just ignore for now — the design doc doesn't mention this.
		_ = comment
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
