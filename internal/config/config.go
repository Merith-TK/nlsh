// Package config loads and provides defaults for the nlsh configuration file.
package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/Merith-TK/nlsh/internal/types"
)

// DefaultConfig returns a Config populated with default values.
func DefaultConfig() types.Config {
	home, _ := os.UserHomeDir()
	return types.Config{
		Provider: types.ProviderConfig{
			Type:  "anthropic",
			Model: "claude-sonnet-4-20250514",
		},
		Prompts: types.PromptsConfig{
			MasterPromptFile: filepath.Join(home, "nlsh_prompt.md"),
		},
		History: types.HistoryConfig{
			File:           filepath.Join(home, "nlsh_history.json"),
			ContextEntries: 25,
		},
		Risk: types.RiskConfig{
			AutoApprove: false,
		},
	}
}

// Load reads ~/.config/nlsh/config.toml if it exists, merging over defaults.
func Load() (types.Config, error) {
	cfg := DefaultConfig()

	home, err := os.UserHomeDir()
	if err != nil {
		return cfg, nil
	}

	path := filepath.Join(home, ".config", "nlsh", "config.toml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}

	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, err
	}

	// Expand ~ in file paths after loading.
	cfg.Prompts.MasterPromptFile = expandHome(cfg.Prompts.MasterPromptFile, home)
	cfg.History.File = expandHome(cfg.History.File, home)

	return cfg, nil
}

func expandHome(path, home string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		return filepath.Join(home, path[2:])
	}
	return path
}
