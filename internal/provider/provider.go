// Package provider handles communication with AI backends.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	openaisdk "github.com/openai/openai-go"
	openaioption "github.com/openai/openai-go/option"

	"github.com/Merith-TK/nlsh/internal/types"
)

// Message is a single turn in the conversation.
type Message struct {
	Role    string // "user" or "assistant"
	Content string
}

// Client is an abstraction over the underlying AI provider.
type Client interface {
	// Complete sends the conversation to the model and returns the assistant reply.
	Complete(ctx context.Context, systemPrompt string, messages []Message) (string, error)
	// ProviderName returns the identifier string for history entries.
	ProviderName() string
	// ModelName returns the model identifier.
	ModelName() string
}

// New constructs a Client based on the provider config and option overrides.
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

// ParseAgentResponse extracts the AgentResponse from raw model output,
// stripping any markdown code fences if present.
func ParseAgentResponse(raw string) (types.AgentResponse, error) {
	s := strings.TrimSpace(raw)
	// Strip ```json ... ``` or ``` ... ``` wrappers
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		// drop first and last lines
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

// --- Anthropic ---

type anthropicClient struct {
	client *anthropicsdk.Client
	model  string
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
	return &anthropicClient{client: &c, model: model}, nil
}

func (a *anthropicClient) Complete(ctx context.Context, systemPrompt string, messages []Message) (string, error) {
	var apiMsgs []anthropicsdk.MessageParam
	for _, m := range messages {
		switch m.Role {
		case "user":
			apiMsgs = append(apiMsgs, anthropicsdk.NewUserMessage(anthropicsdk.NewTextBlock(m.Content)))
		case "assistant":
			apiMsgs = append(apiMsgs, anthropicsdk.NewAssistantMessage(anthropicsdk.NewTextBlock(m.Content)))
		}
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
		return "", fmt.Errorf("anthropic API error: %w", err)
	}
	if len(resp.Content) == 0 {
		return "", fmt.Errorf("anthropic returned empty response")
	}
	return resp.Content[0].Text, nil
}

func (a *anthropicClient) ProviderName() string { return "anthropic" }
func (a *anthropicClient) ModelName() string    { return a.model }

// --- OpenAI-compatible ---

type openaiClient struct {
	client *openaisdk.Client
	model  string
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
	return &openaiClient{client: &c, model: model}, nil
}

func (o *openaiClient) Complete(ctx context.Context, systemPrompt string, messages []Message) (string, error) {
	apiMsgs := []openaisdk.ChatCompletionMessageParamUnion{
		openaisdk.SystemMessage(systemPrompt),
	}
	for _, m := range messages {
		switch m.Role {
		case "user":
			apiMsgs = append(apiMsgs, openaisdk.UserMessage(m.Content))
		case "assistant":
			apiMsgs = append(apiMsgs, openaisdk.AssistantMessage(m.Content))
		}
	}

	resp, err := o.client.Chat.Completions.New(ctx, openaisdk.ChatCompletionNewParams{
		Model:    o.model,
		Messages: apiMsgs,
	})
	if err != nil {
		return "", fmt.Errorf("openai API error: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("openai returned empty response")
	}
	return resp.Choices[0].Message.Content, nil
}

func (o *openaiClient) ProviderName() string { return "openai" }
func (o *openaiClient) ModelName() string    { return o.model }
