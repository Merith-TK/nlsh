// Package harness implements the nlsh-harness REPL and session management.
package harness

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

// Tools exposed to the harness agent.
var (
	presentCommandTool = provider.Tool{
		Name:        "present_command",
		Description: "Present the translated shell command(s) to the user with a risk assessment.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"commands":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"chained":   map[string]any{"type": "boolean"},
				"risk":      map[string]any{"type": "string", "enum": []string{"LOW", "HIGH"}},
				"rationale": map[string]any{"type": "string"},
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
				"path": map[string]any{"type": "string", "description": "Directory path to explore"},
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
				"path": map[string]any{"type": "string", "description": "File path to read"},
			},
			"required": []string{"path"},
		},
	}

	recallHistoryTool = provider.Tool{
		Name:        "recall_history",
		Description: "Search past harness session history for relevant commands or context.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":  map[string]any{"type": "string", "description": "Search query for past sessions"},
				"limit":  map[string]any{"type": "integer", "description": "Max entries to return"},
			},
			"required": []string{"query"},
		},
	}
)

// Session holds the state of an active harness REPL.
type Session struct {
	Client       provider.Client
	Messages     []provider.Message
	SystemPrompt string
	StartTime    time.Time
	Cfg          types.Config
	Turns        []types.SessionTurn
}

// NewSession creates a new harness session.
func NewSession(cfg types.Config) (*Session, error) {
	provCfg := cfg.Provider
	client, err := provider.New(provCfg)
	if err != nil {
		return nil, err
	}

	if provCfg.FallbackTools {
		fmt.Fprintln(os.Stderr, "warning: provider does not support tool-calling; using fallback mode")
	}

	histEntries, _ := history.Last(cfg.History.File, cfg.History.ContextEntries)

	home, _ := os.UserHomeDir()
	reviewFile := home + "/nlsh_review.md"
	systemPrompt := prompt.Assemble(reviewFile, cfg.Prompts.MasterPromptFile, "", histEntries, provCfg.FallbackTools)

	return &Session{
		Client:       client,
		SystemPrompt: systemPrompt,
		StartTime:    time.Now().UTC(),
		Cfg:          cfg,
	}, nil
}

// ProcessOneShot runs a single harness-mode translation with manual confirmation, then exits.
func (s *Session) ProcessOneShot(input string) error {
	ctx := context.Background()
	s.Messages = append(s.Messages, provider.Message{Role: "user", Content: input})
	return s.processTurn(ctx, input)
}

// RunLoop starts the interactive REPL.
func (s *Session) RunLoop() error {
	fmt.Println("nlsh-harness — type 'quit' or press Ctrl+D to exit")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("nlsh-harness> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			// Ctrl+D or EOF
			fmt.Println()
			return s.endSession()
		}

		input := strings.TrimRight(line, "\r\n")
		if input == "" {
			continue
		}
		if strings.ToLower(input) == "quit" || strings.ToLower(input) == "exit" {
			return s.endSession()
		}

		ctx := context.Background()
		s.Messages = append(s.Messages, provider.Message{Role: "user", Content: input})
		if err := s.processTurn(ctx, input); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
}

// processTurn handles a single user input: translate → confirm → execute → manage memory.
func (s *Session) processTurn(ctx context.Context, input string) error {
	for {
		resp, err := s.translate(ctx)
		if err != nil {
			return err
		}

		resp.Risk = applyRiskOverrides(resp, s.Cfg.Risk)
		printTranslation(resp)

		answer, err := confirmPrompt()
		if err != nil {
			return err
		}

		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y":
			outcome, execErr := executeCommands(resp)
			s.Turns = append(s.Turns, types.SessionTurn{
				Prompt:   input,
				Commands: resp.Commands,
				Risk:     resp.Risk,
				Outcome:  outcome,
			})
			s.Messages = append(s.Messages, provider.Message{Role: "assistant", Content: marshalAgentResponse(resp)})
			s.pruneMemory()
			return execErr

		case "n":
			s.Turns = append(s.Turns, types.SessionTurn{
				Prompt:   input,
				Commands: resp.Commands,
				Risk:     resp.Risk,
				Outcome:  types.OutcomeRejected,
			})
			clarification, quitErr := rejectionPrompt()
			if quitErr != nil {
				return quitErr
			}
			s.Messages = append(s.Messages,
				provider.Message{Role: "assistant", Content: marshalAgentResponse(resp)},
				provider.Message{Role: "user", Content: "Rejected. " + clarification},
			)
			continue

		case "q", "quit":
			s.Turns = append(s.Turns, types.SessionTurn{
				Prompt:   input,
				Commands: resp.Commands,
				Risk:     resp.Risk,
				Outcome:  types.OutcomeCanceled,
			})
			s.Messages = append(s.Messages, provider.Message{Role: "assistant", Content: marshalAgentResponse(resp)})
			fmt.Println("Canceled.")
			return nil

		default:
			// Inline refinement.
			s.Turns = append(s.Turns, types.SessionTurn{
				Prompt:     input,
				Commands:   resp.Commands,
				Risk:       resp.Risk,
				Outcome:    types.OutcomeRejected,
				Refinement: answer,
			})
			s.Messages = append(s.Messages,
				provider.Message{Role: "assistant", Content: marshalAgentResponse(resp)},
				provider.Message{Role: "user", Content: answer},
			)
			continue
		}
	}
}

// translate calls the agent, handling filesystem tools and recall_history.
func (s *Session) translate(ctx context.Context) (types.AgentResponse, error) {
	tools := []provider.Tool{presentCommandTool, exploreDirectoryTool, readFileTool, recallHistoryTool}
	if s.Cfg.Provider.FallbackTools {
		tools = nil // fallback: no tools, raw JSON
	}

	for {
		result, err := s.Client.Complete(ctx, s.SystemPrompt, s.Messages, tools)
		if err != nil {
			return types.AgentResponse{}, err
		}

		// Fallback mode: parse raw JSON.
		if s.Cfg.Provider.FallbackTools {
			return provider.ParseAgentResponse(result.Content)
		}

		// Tool mode.
		if result.ToolCall == nil {
			return types.AgentResponse{}, fmt.Errorf("agent did not call a tool; raw: %s", result.Content)
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
				return resp, fmt.Errorf("failed to parse present_command arguments: %w", err)
			}
			return resp, nil

		case "explore_directory":
			path, _ := tc.Arguments["path"].(string)
			res := fs.ExploreDirectory(path, 2) // harness: deeper depth
			content := res.Content
			if res.Error != "" {
				content = fmt.Sprintf("Error: %s", res.Error)
			}
			s.Messages = append(s.Messages,
				provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{*tc}},
				provider.Message{Role: "user", ToolResults: []provider.ToolResult{
					{ToolCallID: toolCallID, Name: "explore_directory", Content: content},
				}},
			)
			continue

		case "read_file":
			path, _ := tc.Arguments["path"].(string)
			res := fs.ReadFile(path, 500, false) // harness: more lines
			content := res.Content
			if res.Error != "" {
				if strings.Contains(res.Error, "Allow reading it for this session") {
					fmt.Println()
					fmt.Println("  ", res.Error)
					if fs.AskSecretPrompt(path) {
						res = fs.ReadFile(path, 500, true)
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
			s.Messages = append(s.Messages,
				provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{*tc}},
				provider.Message{Role: "user", ToolResults: []provider.ToolResult{
					{ToolCallID: toolCallID, Name: "read_file", Content: content},
				}},
			)
			continue

		case "recall_history":
			query, _ := tc.Arguments["query"].(string)
			limitF, _ := tc.Arguments["limit"].(float64)
			limit := int(limitF)
			if limit <= 0 {
				limit = s.Cfg.Harness.RecallLimit
			}
			results := recallHistory(s.Cfg.Harness.HistoryFile, query, limit)
			s.Messages = append(s.Messages,
				provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{*tc}},
				provider.Message{Role: "user", ToolResults: []provider.ToolResult{
					{ToolCallID: toolCallID, Name: "recall_history", Content: results},
				}},
			)
			continue

		default:
			return types.AgentResponse{}, fmt.Errorf("agent called unknown tool %q", tc.Name)
		}
	}
}

func (s *Session) endSession() error {
	s.Turns = append(s.Turns, types.SessionTurn{
		Prompt:  "(session ended)",
		Outcome: types.OutcomeSessionEnd,
	})
	entry := types.SessionHistory{
		Timestamp:    time.Now().UTC(),
		SessionStart: s.StartTime,
		Turns:        s.Turns,
		Outcome:      types.OutcomeSessionEnd,
	}
	if err := writeHarnessHistory(s.Cfg.Harness.HistoryFile, entry); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write harness history: %v\n", err)
	}
	fmt.Println("Session ended.")
	return nil
}

func (s *Session) pruneMemory() {
	maxPairs := s.Cfg.Harness.ContextTurns
	if maxPairs <= 0 {
		maxPairs = 10
	}
	// Each turn = user + assistant pair.
	for len(s.Messages) > maxPairs*2 {
		// Remove oldest pair.
		if len(s.Messages) >= 2 {
			s.Messages = s.Messages[2:]
		}
	}
}

func printTranslation(resp types.AgentResponse) {
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

func confirmPrompt() (string, error) {
	fmt.Print("  Run? [Y/n/...]: ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "q", nil
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func rejectionPrompt() (string, error) {
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
		return "", fmt.Errorf("canceled")
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

func marshalAgentResponse(resp types.AgentResponse) string {
	b, _ := json.Marshal(resp)
	return string(b)
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
