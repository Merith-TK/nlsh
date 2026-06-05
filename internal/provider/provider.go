// Package provider handles communication with AI backends.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	anthropicparam "github.com/anthropics/anthropic-sdk-go/packages/param"
	openaisdk "github.com/openai/openai-go"
	openaioption "github.com/openai/openai-go/option"

	"github.com/Merith-TK/nlsh/internal/types"
)

// Tool defines a tool the agent may call.
type Tool struct {
	Name        string
	Description string
	// Schema is a JSON Schema object describing the tool's parameters.
	Schema map[string]any
}

// ToolCall represents a tool invocation from the assistant.
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// ToolResult is sent back to the model after executing a tool.
type ToolResult struct {
	ToolCallID string
	Name       string
	Content    string // JSON string
}

// Message is a single turn in the conversation.
type Message struct {
	Role        string // "user" or "assistant"
	Content     string
	ToolCalls   []ToolCall   // populated for assistant messages that called tools
	ToolResults []ToolResult // populated for user messages that are tool results
}

// CompletionResult is the model's reply.
type CompletionResult struct {
	Content  string    // normal text reply (if no tool called)
	ToolCall *ToolCall // if the assistant invoked a tool
}

// Client is an abstraction over the underlying AI provider.
type Client interface {
	// Complete sends the conversation to the model. Tools may be nil.
	Complete(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*CompletionResult, error)
	// ProviderName returns the identifier string for history entries.
	ProviderName() string
	// ModelName returns the model identifier.
	ModelName() string
}

// New constructs a Client based on the provider config.
func New(cfg types.ProviderConfig) (Client, error) {
	providerType := cfg.Type
	if providerType == "" {
		providerType = "anthropic"
	}

	switch strings.ToLower(providerType) {
	case "anthropic":
		return newAnthropicClient(cfg)
	case "openai":
		return newOpenAIClient(cfg)
	default:
		return nil, fmt.Errorf("unknown provider type %q; supported: anthropic, openai", providerType)
	}
}

// --- Fallback JSON parsing (shared by both providers in fallback mode) ---

// ParseAgentResponse extracts AgentResponse from raw model text.
func ParseAgentResponse(raw string) (types.AgentResponse, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) >= 2 {
			s = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	var resp types.AgentResponse
	if err := json.Unmarshal([]byte(s), &resp); err != nil {
		return resp, fmt.Errorf("invalid agent JSON: %w\nraw: %s", err, raw)
	}
	return resp, nil
}

// ParseFallbackToolCall attempts to parse a tool call from raw JSON.
// Expected format: {"tool_call": {"name": "...", "arguments": {...}}}
func ParseFallbackToolCall(raw string) (*ToolCall, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) >= 2 {
			s = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	var wrapper struct {
		ToolCall struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"tool_call"`
	}
	if err := json.Unmarshal([]byte(s), &wrapper); err != nil {
		return nil, err
	}
	if wrapper.ToolCall.Name == "" {
		return nil, fmt.Errorf("no tool_call found")
	}
	return &ToolCall{
		Name:      wrapper.ToolCall.Name,
		Arguments: wrapper.ToolCall.Arguments,
	}, nil
}

// --- Anthropic ---

type anthropicClient struct {
	client *anthropicsdk.Client
	model  string
	fallback bool
}

func newAnthropicClient(cfg types.ProviderConfig) (*anthropicClient, error) {
	opts := []option.RequestOption{}
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	c := anthropicsdk.NewClient(opts...)
	model := cfg.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	return &anthropicClient{client: &c, model: model, fallback: cfg.FallbackTools}, nil
}

func (a *anthropicClient) Complete(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*CompletionResult, error) {
	if a.fallback || len(tools) == 0 {
		return a.completeFallback(ctx, systemPrompt, messages)
	}
	return a.completeTool(ctx, systemPrompt, messages, tools)
}

func (a *anthropicClient) completeFallback(ctx context.Context, systemPrompt string, messages []Message) (*CompletionResult, error) {
	var apiMsgs []anthropicsdk.MessageParam
	for _, m := range messages {
		apiMsgs = append(apiMsgs, a.toAnthropicMessage(m))
	}

	resp, err := a.client.Messages.New(ctx, anthropicsdk.MessageNewParams{
		Model:     anthropicsdk.Model(a.model),
		MaxTokens: 1024,
		System: []anthropicsdk.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: apiMsgs,
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic API error: %w", err)
	}
	if len(resp.Content) == 0 {
		return nil, fmt.Errorf("anthropic returned empty response")
	}

	text := resp.Content[0].Text
	// Try parsing as a tool call first.
	if tc, err := ParseFallbackToolCall(text); err == nil {
		return &CompletionResult{ToolCall: tc}, nil
	}
	return &CompletionResult{Content: text}, nil
}

func (a *anthropicClient) completeTool(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*CompletionResult, error) {
	var apiMsgs []anthropicsdk.MessageParam
	for _, m := range messages {
		apiMsgs = append(apiMsgs, a.toAnthropicMessage(m))
	}

	var apiTools []anthropicsdk.ToolUnionParam
	for _, t := range tools {
		apiTools = append(apiTools, anthropicsdk.ToolUnionParam{
			OfTool: &anthropicsdk.ToolParam{
				Name:        t.Name,
				Description: anthropicparam.NewOpt(t.Description),
				InputSchema: anthropicsdk.ToolInputSchemaParam{
					Properties: t.Schema["properties"],
					Required:   toStringSlice(t.Schema["required"]),
				},
				Type: anthropicsdk.ToolTypeCustom,
			},
		})
	}

	resp, err := a.client.Messages.New(ctx, anthropicsdk.MessageNewParams{
		Model:     anthropicsdk.Model(a.model),
		MaxTokens: 1024,
		System: []anthropicsdk.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: apiMsgs,
		Tools:    apiTools,
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic API error: %w", err)
	}
	if len(resp.Content) == 0 {
		return nil, fmt.Errorf("anthropic returned empty response")
	}

	// Check for tool_use in the first content block.
	block := resp.Content[0]
	if block.Type == "tool_use" {
		var args map[string]any
		if err := json.Unmarshal(block.Input, &args); err != nil {
			return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
		}
		return &CompletionResult{
			ToolCall: &ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: args,
			},
		}, nil
	}

	return &CompletionResult{Content: block.Text}, nil
}

func (a *anthropicClient) toAnthropicMessage(m Message) anthropicsdk.MessageParam {
	if m.Role == "assistant" {
		if len(m.ToolCalls) > 0 {
			var blocks []anthropicsdk.ContentBlockParamUnion
			for _, tc := range m.ToolCalls {
				inputJSON, _ := json.Marshal(tc.Arguments)
				blocks = append(blocks, anthropicsdk.NewToolUseBlock(tc.ID, json.RawMessage(inputJSON), tc.Name))
			}
			return anthropicsdk.NewAssistantMessage(blocks...)
		}
		return anthropicsdk.NewAssistantMessage(anthropicsdk.NewTextBlock(m.Content))
	}

	// User message.
	if len(m.ToolResults) > 0 {
		var blocks []anthropicsdk.ContentBlockParamUnion
		for _, tr := range m.ToolResults {
			blocks = append(blocks, anthropicsdk.NewToolResultBlock(tr.ToolCallID, tr.Content, false))
		}
		return anthropicsdk.NewUserMessage(blocks...)
	}
	return anthropicsdk.NewUserMessage(anthropicsdk.NewTextBlock(m.Content))
}

func (a *anthropicClient) ProviderName() string { return "anthropic" }
func (a *anthropicClient) ModelName() string    { return a.model }

// --- OpenAI-compatible ---

type openaiClient struct {
	client   *openaisdk.Client
	model    string
	fallback bool
}

func newOpenAIClient(cfg types.ProviderConfig) (*openaiClient, error) {
	opts := []openaioption.RequestOption{}
	if cfg.APIKey != "" {
		opts = append(opts, openaioption.WithAPIKey(cfg.APIKey))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, openaioption.WithBaseURL(cfg.BaseURL))
	}
	c := openaisdk.NewClient(opts...)
	model := cfg.Model
	if model == "" {
		model = "gpt-4o"
	}
	return &openaiClient{client: &c, model: model, fallback: cfg.FallbackTools}, nil
}

func (o *openaiClient) Complete(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*CompletionResult, error) {
	if o.fallback || len(tools) == 0 {
		return o.completeFallback(ctx, systemPrompt, messages)
	}
	return o.completeTool(ctx, systemPrompt, messages, tools)
}

func (o *openaiClient) completeFallback(ctx context.Context, systemPrompt string, messages []Message) (*CompletionResult, error) {
	apiMsgs := []openaisdk.ChatCompletionMessageParamUnion{
		openaisdk.SystemMessage(systemPrompt),
	}
	for _, m := range messages {
		apiMsgs = append(apiMsgs, o.toOpenAIMessage(m))
	}

	resp, err := o.client.Chat.Completions.New(ctx, openaisdk.ChatCompletionNewParams{
		Model:    o.model,
		Messages: apiMsgs,
	})
	if err != nil {
		return nil, fmt.Errorf("openai API error: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai returned empty response")
	}

	text := resp.Choices[0].Message.Content
	if tc, err := ParseFallbackToolCall(text); err == nil {
		return &CompletionResult{ToolCall: tc}, nil
	}
	return &CompletionResult{Content: text}, nil
}

func (o *openaiClient) completeTool(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*CompletionResult, error) {
	apiMsgs := []openaisdk.ChatCompletionMessageParamUnion{
		openaisdk.SystemMessage(systemPrompt),
	}
	for _, m := range messages {
		apiMsgs = append(apiMsgs, o.toOpenAIMessage(m))
	}

	var apiTools []openaisdk.ChatCompletionToolParam
	for _, t := range tools {
		schemaJSON, _ := json.Marshal(t.Schema)
		var params map[string]any
		json.Unmarshal(schemaJSON, &params)
		apiTools = append(apiTools, openaisdk.ChatCompletionToolParam{
			Function: openaisdk.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openaisdk.Opt(t.Description),
				Parameters:  openaisdk.FunctionParameters(params),
			},
		})
	}

	resp, err := o.client.Chat.Completions.New(ctx, openaisdk.ChatCompletionNewParams{
		Model:    o.model,
		Messages: apiMsgs,
		Tools:    apiTools,
	})
	if err != nil {
		return nil, fmt.Errorf("openai API error: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai returned empty response")
	}

	msg := resp.Choices[0].Message
	if len(msg.ToolCalls) > 0 {
		 tc := msg.ToolCalls[0]
		 var args map[string]any
		 if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			 return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
		 }
		 return &CompletionResult{
			 ToolCall: &ToolCall{
				 ID:        tc.ID,
				 Name:      tc.Function.Name,
				 Arguments: args,
			 },
		 }, nil
	}

	return &CompletionResult{Content: msg.Content}, nil
}

func (o *openaiClient) toOpenAIMessage(m Message) openaisdk.ChatCompletionMessageParamUnion {
	if m.Role == "assistant" {
		if len(m.ToolCalls) > 0 {
			var toolCalls []openaisdk.ChatCompletionMessageToolCallParam
			for _, tc := range m.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Arguments)
				toolCalls = append(toolCalls, openaisdk.ChatCompletionMessageToolCallParam{
					ID: tc.ID,
					Function: openaisdk.ChatCompletionMessageToolCallFunctionParam{
						Name:      tc.Name,
						Arguments: string(argsJSON),
					},
				})
			}
			return openaisdk.ChatCompletionMessageParamUnion{
				OfAssistant: &openaisdk.ChatCompletionAssistantMessageParam{
					ToolCalls: toolCalls,
				},
			}
		}
		return openaisdk.AssistantMessage(m.Content)
	}

	if len(m.ToolResults) > 0 {
		// OpenAI uses separate tool messages for each result.
		if len(m.ToolResults) == 1 {
			return openaisdk.ToolMessage(m.ToolResults[0].Content, m.ToolResults[0].ToolCallID)
		}
		// Multiple tool results: OpenAI expects one tool message per result.
		// For simplicity, concatenate them into one message (not ideal but works for most endpoints).
		var parts []string
		for _, tr := range m.ToolResults {
			parts = append(parts, fmt.Sprintf("[%s] %s", tr.Name, tr.Content))
		}
		return openaisdk.UserMessage(strings.Join(parts, "\n"))
	}
	return openaisdk.UserMessage(m.Content)
}

func (o *openaiClient) ProviderName() string { return "openai" }
func (o *openaiClient) ModelName() string    { return o.model }

func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	if arr, ok := v.([]string); ok {
		return arr
	}
	if arr, ok := v.([]any); ok {
		var out []string
		for _, item := range arr {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
