// Package benchmark runs head-to-head model comparisons for nlsh.
package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Merith-TK/nlsh/internal/fs"
	"github.com/Merith-TK/nlsh/internal/prompt"
	"github.com/Merith-TK/nlsh/internal/provider"
	"github.com/Merith-TK/nlsh/internal/types"
)

// StandardQueries is the default benchmark query suite.
var StandardQueries = []string{
	"list files",
	"show me disk usage",
	"compile this go project",
	"what build system does this project use",
}

// Result is a single benchmark run.
type Result struct {
	Model           string
	Query           string
	Duration        time.Duration
	Error           string
	Commands        []string
	Chained         bool
	Risk            string
	Rationale       string
	ToolCompliant   bool // true if agent used present_command tool
	Explored        bool // true if agent used filesystem tools
}

// Run executes the benchmark suite against the given models.
func Run(cfg types.Config, models []string, queries []string) error {
	if len(models) == 0 {
		return fmt.Errorf("no models specified; use --models model1,model2")
	}
	if len(queries) == 0 {
		queries = StandardQueries
	}

	fmt.Println("nlsh Model Benchmark")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()

	// Build system prompt once.
	home, _ := os.UserHomeDir()
	reviewFile := home + "/nlsh_review.md"
	systemPrompt := prompt.Assemble(reviewFile, cfg.Prompts.MasterPromptFile, "", nil, cfg.Provider.FallbackTools)

	var allResults []Result

	for _, model := range models {
		fmt.Printf("Model: %s\n", model)
		fmt.Println(strings.Repeat("-", 40))

		for _, query := range queries {
			fmt.Printf("  Query: %q\n", query)
			result := benchModel(cfg, model, systemPrompt, query)
			allResults = append(allResults, result)

			if result.Error != "" {
				fmt.Printf("    ERROR: %s\n", result.Error)
			} else {
				cmdStr := strings.Join(result.Commands, " && ")
				if result.Chained {
					cmdStr = strings.Join(result.Commands, " && ")
				}
				fmt.Printf("    Time: %.2fs | Tool: %v | Risk: %s\n",
					result.Duration.Seconds(),
					result.ToolCompliant,
					result.Risk,
				)
				fmt.Printf("    Command: %s\n", cmdStr)
				if result.Explored {
					fmt.Printf("    Note: Agent explored workspace\n")
				}
			}
			fmt.Println()
		}
		fmt.Println()
	}

	printSummary(allResults)
	return nil
}

func benchModel(cfg types.Config, model, systemPrompt, query string) Result {
	result := Result{Model: model, Query: query}

	provCfg := cfg.Provider
	provCfg.Model = model

	client, err := provider.New(provCfg)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	ctx := context.Background()
	messages := []provider.Message{{Role: "user", Content: query}}

	start := time.Now()

	// Replicate the tool-calling loop inline to get full observability.
	explored := false
	fallback := provCfg.FallbackTools
	tools := []provider.Tool{
		presentCommandTool,
		exploreDirectoryTool,
		readFileTool,
	}
	if fallback {
		tools = nil
	}

	var resp types.AgentResponse
	var toolCompliant bool

	for attempts := 0; attempts < 10; attempts++ {
		res, err := client.Complete(ctx, systemPrompt, messages, tools)
		if err != nil {
			result.Error = err.Error()
			result.Duration = time.Since(start)
			return result
		}

		if fallback {
			resp, err = provider.ParseAgentResponse(res.Content)
			if err != nil {
				result.Error = err.Error()
			} else {
				toolCompliant = false // fallback means no tool was used
			}
			break
		}

		if res.ToolCall == nil {
			result.Error = fmt.Sprintf("agent did not call a tool; raw: %s", res.Content)
			break
		}

		tc := res.ToolCall
		toolCallID := tc.ID
		if toolCallID == "" {
			toolCallID = fmt.Sprintf("%s_1", tc.Name)
		}

		switch tc.Name {
		case "present_command":
			argsJSON, _ := json.Marshal(tc.Arguments)
			if err := json.Unmarshal(argsJSON, &resp); err != nil {
				result.Error = fmt.Sprintf("failed to parse present_command: %v", err)
			} else {
				toolCompliant = true
			}
			goto done

		case "explore_directory":
			explored = true
			path, _ := tc.Arguments["path"].(string)
			fsRes := fs.ExploreDirectory(path, 1)
			content := fsRes.Content
			if fsRes.Error != "" {
				content = fmt.Sprintf("Error: %s", fsRes.Error)
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
			fsRes := fs.ReadFile(path, 100, false)
			content := fsRes.Content
			if fsRes.Error != "" {
				if strings.Contains(fsRes.Error, "Allow reading it for this session") {
					// In benchmark mode, auto-decline secret files.
					content = "User declined to allow reading this file."
				} else {
					content = fmt.Sprintf("Error: %s", fsRes.Error)
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
			result.Error = fmt.Sprintf("unknown tool %q", tc.Name)
			goto done
		}
	}

 done:
	result.Duration = time.Since(start)
	result.Commands = resp.Commands
	result.Chained = resp.Chained
	result.Risk = resp.Risk
	result.Rationale = resp.Rationale
	result.ToolCompliant = toolCompliant
	result.Explored = explored
	return result
}

func printSummary(results []Result) {
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("Summary")
	fmt.Println(strings.Repeat("-", 40))

	modelStats := make(map[string]struct{ total, success, toolOK time.Duration; count int })
	for _, r := range results {
		if r.Error != "" {
			continue
		}
		stats := modelStats[r.Model]
		stats.total += r.Duration
		stats.success += r.Duration
		if r.ToolCompliant {
			stats.toolOK += r.Duration
		}
		stats.count++
		modelStats[r.Model] = stats
	}

	for model, stats := range modelStats {
		avg := stats.total / time.Duration(stats.count)
		fmt.Printf("  %-25s avg: %.2fs | runs: %d\n", model, avg.Seconds(), stats.count)
	}
	fmt.Println()
}

// Tool definitions (duplicated from run for independence).
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
		Description: "List the contents of a directory. Limited depth.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
	}

	readFileTool = provider.Tool{
		Name:        "read_file",
		Description: "Read a text file with line limits.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
	}
)
