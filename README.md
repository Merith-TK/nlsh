# nlsh — Natural Language Shell

Translate natural language into shell commands, with a smart risk gate and optional confirmation.

## Install

```bash
go install github.com/Merith-TK/nlsh@latest
```

Or clone and build:

```bash
git clone https://github.com/Merith-TK/nlsh.git
cd nlsh && go build -o nlsh .
```

## Quick Start

```bash
# One-shot
nlsh "show me running docker containers"

# Interactive REPL
nlsh harness

# Dry-run (show command, don't execute)
nlsh --dry-run "restart the nginx service"
```

## Configuration

Create `~/.config/nlsh/config.toml`:

```toml
[provider]
type     = "openai"              # or "anthropic"
model    = "deepseek-v4-flash"
api_key  = "your-api-key"
base_url = "https://your-endpoint/v1"

# For unsupported endpoints, use fallback mode
fallback_tools = false

[harness]
context_turns = 10
recall_limit  = 5
```

## Commands

| Command | Description |
|---|---|
| `nlsh "<input>"` | One-shot translate and execute |
| `nlsh harness` | Start interactive REPL |
| `nlsh review` | Analyze command history |
| `nlsh benchmark --models m1,m2` | Compare models head-to-head |

## Flags

| Flag | Description |
|---|---|
| `--dry-run` | Show command, never execute |
| `--yes` / `-y` | Auto-approve HIGH risk commands |
| `--llm-model` | Override model for this run |
| `--provider` | Override provider (anthropic/openai) |
| `--no-history` | Skip reading/writing history |
| `--plain` | Suppress styling |

## How It Works

1. **Translate** — AI converts your natural language into shell commands
2. **Risk Gate** — LOW risk auto-executes, HIGH risk prompts for confirmation
3. **Confirm** — `y` to run, `n` to refine, `q` to cancel, or type natural language to tweak
4. **History** — Every command is logged to `~/.nlsh_history.json`

## License

MIT
