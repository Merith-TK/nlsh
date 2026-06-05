// Package types defines the shared data structures used across nlsh.
package types

import "time"

// AgentResponse is the JSON contract returned by the AI agent.
type AgentResponse struct {
	Commands  []string `json:"commands"`
	Chained   bool     `json:"chained"`
	Risk      string   `json:"risk"` // "LOW" or "HIGH"
	Rationale string   `json:"rationale"`
}

// Outcome values for history entries.
const (
	OutcomeApproved   = "approved"
	OutcomeRejected   = "rejected"
	OutcomeCanceled   = "canceled"
	OutcomeSessionEnd = "session_ended"
)

// HistoryEntry is a single record written to ~/nlsh_history.json.
type HistoryEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Prompt    string    `json:"prompt"`
	Commands  []string  `json:"commands"`
	Risk      string    `json:"risk"`
	Outcome   string    `json:"outcome"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
}

// SessionTurn is a single turn within a harness session.
type SessionTurn struct {
	Prompt     string   `json:"prompt"`
	Commands   []string `json:"commands"`
	Risk       string   `json:"risk"`
	Outcome    string   `json:"outcome"`
	Refinement string   `json:"refinement,omitempty"`
}

// SessionHistory is a full harness session record written to disk.
type SessionHistory struct {
	Timestamp    time.Time     `json:"timestamp"`
	SessionStart time.Time     `json:"session_start"`
	Turns        []SessionTurn `json:"turns"`
	Outcome      string        `json:"outcome"`
}

// Config mirrors the TOML config file structure.
type Config struct {
	Provider ProviderConfig `toml:"provider"`
	Prompts  PromptsConfig  `toml:"prompts"`
	History  HistoryConfig  `toml:"history"`
	Risk     RiskConfig     `toml:"risk"`
	Harness  HarnessConfig  `toml:"harness"`
}

// ProviderConfig holds AI provider settings.
type ProviderConfig struct {
	Type          string `toml:"type"`    // "anthropic" or "openai"
	Model         string `toml:"model"`
	APIKey        string `toml:"api_key"`
	BaseURL       string `toml:"base_url"`
	FallbackTools bool   `toml:"fallback_tools"` // unsupported endpoint mode
}

// PromptsConfig holds prompt file paths.
type PromptsConfig struct {
	MasterPromptFile string `toml:"master_prompt_file"`
}

// HistoryConfig holds one-shot history settings.
type HistoryConfig struct {
	File           string `toml:"file"`
	ContextEntries int    `toml:"context_entries"`
}

// HarnessConfig holds harness-specific settings.
type HarnessConfig struct {
	ContextTurns int    `toml:"context_turns"`
	RecallLimit  int    `toml:"recall_limit"`
	HistoryFile  string `toml:"history_file"`
	AutoApprove  bool   `toml:"auto_approve"`
}

// RiskConfig holds risk override settings.
type RiskConfig struct {
	AutoApprove       bool     `toml:"auto_approve"`
	LowRiskOverrides  []string `toml:"low_risk_overrides"`
	HighRiskOverrides []string `toml:"high_risk_overrides"`
}

// RunOptions holds the parsed CLI flags for a single invocation.
type RunOptions struct {
	Input       string
	DryRun      bool
	AutoApprove bool
	Provider    string
	Model       string
	Prompt      string
	NoHistory   bool
	Plain       bool
}
