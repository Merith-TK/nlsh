// Package fs tools implements explore_directory and read_file.
package fs

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ToolResult is returned after executing a filesystem tool.
type ToolResult struct {
	Content string
	Error   string
}

// ExploreDirectory lists the contents of a directory.
func ExploreDirectory(path string, maxDepth int) *ToolResult {
	enforcer, err := NewEnforcer()
	if err != nil {
		return &ToolResult{Error: fmt.Sprintf("enforcer init failed: %v", err)}
	}

	level, reason := enforcer.CheckPath(path)
	if level == AccessBlock {
		return &ToolResult{Error: fmt.Sprintf("access denied: %s", reason)}
	}

	abs, _ := filepath.Abs(path)
	abs = filepath.Clean(abs)

	if maxDepth <= 0 {
		maxDepth = 1
	}

	// Use find for depth control, fallback to ls.
	cmd := exec.Command("find", abs, "-maxdepth", fmt.Sprintf("%d", maxDepth), "-not", "-path", "*/\\.*", "-print")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback to ls -la
		cmd = exec.Command("ls", "-la", abs)
		out, err = cmd.CombinedOutput()
		if err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to list directory: %v\n%s", err, string(out))}
		}
	}

	return &ToolResult{Content: string(out)}
}

// ReadFile reads a text file with line limits and secret filtering.
func ReadFile(path string, maxLines int, allowSecret bool) *ToolResult {
	enforcer, err := NewEnforcer()
	if err != nil {
		return &ToolResult{Error: fmt.Sprintf("enforcer init failed: %v", err)}
	}

	level, reason := enforcer.CheckPath(path)
	if level == AccessBlock {
		return &ToolResult{Error: fmt.Sprintf("access denied: %s", reason)}
	}

	abs, _ := filepath.Abs(path)
	abs = filepath.Clean(abs)

	if level == AccessFilter && !allowSecret {
		// Scan for secrets before returning anything.
		scan, err := ScanFile(abs, maxLines)
		if err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to scan file: %v", err)}
		}
		if !scan.Clean {
			return &ToolResult{
				Error: fmt.Sprintf(
					"file '%s' may contain sensitive data. Allow reading it for this session? [y/N]", abs),
			}
		}
		// Clean file, return redacted content (which is the original since nothing was found).
		return &ToolResult{Content: scan.Redacted}
	}

	// ALLOW or user already approved secrets.
	content, err := readFileLines(abs, maxLines)
	if err != nil {
		return &ToolResult{Error: fmt.Sprintf("failed to read file: %v", err)}
	}
	return &ToolResult{Content: content}
}

func readFileLines(path string, maxLines int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() && count < maxLines {
		lines = append(lines, scanner.Text())
		count++
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	return strings.Join(lines, "\n"), nil
}

// AskSecretPrompt presents the interactive prompt for secret-containing files.
func AskSecretPrompt(path string) bool {
	fmt.Printf("The file %s may contain sensitive data.\nAllow reading it for this session? [y/N]: ", path)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimRight(line, "\r\n"))
	return answer == "y" || answer == "yes"
}
