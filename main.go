package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Merith-TK/nlsh/internal/config"
	"github.com/Merith-TK/nlsh/internal/history"
	"github.com/Merith-TK/nlsh/internal/review"
	"github.com/Merith-TK/nlsh/internal/run"
	"github.com/Merith-TK/nlsh/internal/types"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fatalf("config error: %v", err)
	}

	// Apply env overrides.
	if v := os.Getenv("NLSH_PROVIDER"); v != "" && cfg.Provider.Type == "" {
		cfg.Provider.Type = v
	}
	if v := os.Getenv("NLSH_MODEL"); v != "" && cfg.Provider.Model == "" {
		cfg.Provider.Model = v
	}
	if v := os.Getenv("NLSH_AUTO_APPROVE"); v == "1" {
		cfg.Risk.AutoApprove = true
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" && cfg.Provider.APIKey == "" {
		cfg.Provider.APIKey = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" && cfg.Provider.APIKey == "" {
		cfg.Provider.APIKey = v
	}

	args := os.Args[1:]

	// --- nlsh review subcommand ---
	if len(args) > 0 && args[0] == "review" {
		reviewFlags := flag.NewFlagSet("review", flag.ExitOnError)
		dryRun := reviewFlags.Bool("dry-run", false, "Print review output to stdout, don't write file")
		show := reviewFlags.Bool("show", false, "Print current review prompt to stdout")
		clear := reviewFlags.Bool("clear", false, "Delete the review prompt file")
		reviewFlags.Parse(args[1:])

		if err := review.Run(cfg, *dryRun, *show, *clear); err != nil {
			fatalf("%v", err)
		}
		return
	}

	// --- History utility flags (top-level) ---
	// Detect early flags before full parse.
	for _, a := range args {
		if a == "--clear-history" {
			if err := history.Clear(cfg.History.File); err != nil {
				fatalf("could not clear history: %v", err)
			}
			fmt.Println("History cleared.")
			os.Exit(0)
		}
		if a == "--history" {
			entries, err := history.Load(cfg.History.File)
			if err != nil {
				fatalf("could not read history: %v", err)
			}
			for _, e := range entries {
				fmt.Printf("[%s] %s | %s | %s\n",
					e.Timestamp.Format("2006-01-02T15:04:05Z"),
					e.Outcome,
					e.Risk,
					e.Prompt,
				)
			}
			os.Exit(0)
		}
	}

	// --- Main translate + execute path ---
	fs := flag.NewFlagSet("nlsh", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Translate and show commands, never execute")
	autoApprove := fs.Bool("yes", false, "Auto-approve all HIGH risk commands")
	fs.Bool("y", false, "Auto-approve all HIGH risk commands (short)")
	providerFlag := fs.String("provider", "", "Override provider (anthropic|openai)")
	modelFlag := fs.String("model", "", "Override model")
	promptFlag := fs.String("prompt", "", "Append a one-off instruction to the master prompt")
	noHistory := fs.Bool("no-history", false, "Skip reading and writing history")
	plain := fs.Bool("plain", false, "Suppress styling")
	fs.Parse(args)

	// -y shorthand
	yFlag := fs.Lookup("y")
	if yFlag != nil && yFlag.Value.String() == "true" {
		*autoApprove = true
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		fatalf("usage: nlsh [flags] \"<natural language input>\"\n       nlsh review [flags]")
	}

	input := strings.Join(remaining, " ")

	opts := types.RunOptions{
		Input:       input,
		DryRun:      *dryRun,
		AutoApprove: *autoApprove,
		Provider:    *providerFlag,
		Model:       *modelFlag,
		Prompt:      *promptFlag,
		NoHistory:   *noHistory,
		Plain:       *plain,
	}

	if err := run.Execute(cfg, opts); err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "nlsh: "+format+"\n", args...)
	os.Exit(1)
}
