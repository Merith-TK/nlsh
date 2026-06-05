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
	OutcomeApproved = "approved"
	OutcomeRejected = "rejected"
	OutcomeCanceled = "canceled"
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

// Config mirrors the TOML config file structure.
type Config struct {
	Provider ProviderConfig `toml:"provider"`
	Prompts  PromptsConfig  `toml:"prompts"`
	History  HistoryConfig  `toml:"history"`
	Risk     RiskConfig     `toml:"risk"`
}

// ProviderConfig holds AI provider settings.
type ProviderConfig struct {
	Type    string `toml:"type"`    // "anthropic" or "openai"
	Model   string `toml:"model"`
	APIKey  string `toml:"api_key"`
	BaseURL string `toml:"base_url"`
}

// PromptsConfig holds prompt file paths.
type PromptsConfig struct {
	MasterPromptFile string `toml:"master_prompt_file"`
}

// HistoryConfig holds history settings.
type HistoryConfig struct {
	File           string `toml:"file"`
	ContextEntries int    `toml:"context_entries"`
}

// RiskConfig holds risk override settings.
type RiskConfig struct {
	AutoApprove        bool     `toml:"auto_approve"`
	LowRiskOverrides   []string `toml:"low_risk_overrides"`
	HighRiskOverrides  []string `toml:"high_risk_overrides"`
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
