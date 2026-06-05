package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Merith-TK/nlsh/internal/config"
	"github.com/Merith-TK/nlsh/internal/harness"
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

	if len(args) == 0 {
		printHelp()
		os.Exit(0)
	}

	// Detect subcommand by lowercasing arg 0.
	sub := strings.ToLower(args[0])

	switch sub {
	case "review":
		reviewCmd(cfg, args[1:])
	case "harness":
		harnessCmd(cfg, args[1:])
	default:
		// One-shot mode: all args are the natural language input.
		oneShotCmd(cfg, args)
	}
}

func reviewCmd(cfg types.Config, args []string) {
	fs := flag.NewFlagSet("review", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Print review output to stdout, don't write file")
	show := fs.Bool("show", false, "Print current review prompt to stdout")
	clear := fs.Bool("clear", false, "Delete the review prompt file")
	oneShotOnly := fs.Bool("one-shot-only", false, "Review only one-shot history")
	harnessOnly := fs.Bool("harness-only", false, "Review only harness history")
	fs.Parse(args)

	if err := review.Run(cfg, *dryRun, *show, *clear, *oneShotOnly, *harnessOnly); err != nil {
		fatalf("%v", err)
	}
}

func harnessCmd(cfg types.Config, args []string) {
	if len(args) == 0 {
		// REPL mode.
		session, err := harness.NewSession(cfg)
		if err != nil {
			fatalf("%v", err)
		}
		if err := session.RunLoop(); err != nil {
			fatalf("%v", err)
		}
		return
	}

	// Harness one-shot: manual confirm, no auto-approve.
	input := strings.Join(args, " ")
	session, err := harness.NewSession(cfg)
	if err != nil {
		fatalf("%v", err)
	}
	if err := session.ProcessOneShot(input); err != nil {
		fatalf("%v", err)
	}
}

func oneShotCmd(cfg types.Config, args []string) {
	// Detect early history flags before full parse.
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
		fatalf("usage: nlsh [flags] \"<natural language input>\"\n       nlsh harness [flags]\n       nlsh review [flags]")
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

func printHelp() {
	fmt.Println(`nlsh — Natural Language Shell

Usage:
  nlsh "<natural language input>"     One-shot translate and execute
  nlsh harness                        Start interactive harness REPL
  nlsh harness "<input>"              Harness one-shot (manual confirm)
  nlsh review                         Review both histories
  nlsh review --one-shot-only         Review only one-shot history
  nlsh review --harness-only          Review only harness history
  nlsh --history                      Print one-shot history
  nlsh --clear-history                Clear one-shot history

Flags:
  --dry-run        Translate and show, never execute
  --yes, -y        Auto-approve HIGH risk commands
  --provider       Override provider (anthropic|openai)
  --model          Override model
  --prompt         Append one-off instruction to master prompt
  --no-history     Skip reading and writing history
  --plain          Suppress styling`)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "nlsh: "+format+"\n", args...)
	os.Exit(1)
}
