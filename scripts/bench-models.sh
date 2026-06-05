#!/usr/bin/env bash
# nlsh-model-bench.sh — Quick benchmark of available models on OpenCode Go

set -e

MODELS=(
  "deepseek-v4-flash"
  "qwen3.5-plus"
  "qwen3.6-plus"
  "minimax-m2.5"
  "mimo-v2.5"
)

QUERIES=(
  "list files"
  "compile this go project"
  "show me disk usage"
  "what build system does this project use"
)

echo "nlsh Model Benchmark"
echo "===================="
echo ""

for model in "${MODELS[@]}"; do
  echo "=== Model: $model ==="
  for query in "${QUERIES[@]}"; do
    echo "  Query: '$query'"
    start=$(date +%s.%N)
    result=$(nlsh --dry-run --no-history --llm-model "$model" "$query" 2>&1) || {
      echo "    ERROR: $result"
      continue
    }
    end=$(date +%s.%N)
    duration=$(python3 -c "print(f'{$end - $start:.2f}s')" 2>/dev/null || echo "unknown")
    
    # Extract command from result
    cmd=$(echo "$result" | grep -A1 "Translated:" | tail -1 | xargs)
    risk=$(echo "$result" | grep "Risk:" | head -1 | sed 's/.*Risk: //' | cut -d' ' -f1)
    
    echo "    Time: ${duration} | Command: $cmd | Risk: $risk"
  done
  echo ""
done

echo "Benchmark complete."
