// Package fs scanner detects secrets in file contents before the agent sees them.
package fs

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// secretPatterns are regexes for common secret formats.
var secretPatterns = []*regexp.Regexp{
	// API keys
	regexp.MustCompile(`\bsk-[a-zA-Z0-9]{20,}\b`),                    // OpenAI-style
	regexp.MustCompile(`\bghp_[a-zA-Z0-9]{36}\b`),                    // GitHub PAT
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),                       // AWS Access Key ID
	regexp.MustCompile(`\bgho_[a-zA-Z0-9]{36}\b`),                    // GitHub OAuth
	regexp.MustCompile(`\bglpat-[a-zA-Z0-9\-]{20}\b`),               // GitLab PAT
	regexp.MustCompile(`\b[0-9a-zA-Z]{32,}\b`),                      // Generic 32+ char hex/base64
}

// secretKeywords trigger FILTER even if no regex matches.
var secretKeywords = []string{
	"api_key", "apikey", "api-key",
	"secret", "secret_key", "secretkey",
	"token", "access_token", "auth_token",
	"password", "passwd", "pwd",
	"private_key", "privatekey",
	"credential", "credentials",
	"bearer ", "basic ",
}

// ScanResult is the outcome of scanning a file.
type ScanResult struct {
	Clean       bool
	Detections  []string // Human-readable findings
	Redacted    string   // Content with secrets replaced
}

// ScanFile reads a file and checks for secrets.
func ScanFile(path string, maxLines int) (*ScanResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() && lineNum < maxLines {
		lines = append(lines, scanner.Text())
		lineNum++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	result := &ScanResult{Clean: true}
	for i, line := range lines {
		processed := scanLine(line)
		if processed.Found {
			result.Clean = false
			result.Detections = append(result.Detections, fmt.Sprintf("line %d: %s", i+1, processed.Reason))
			lines[i] = processed.Redacted
		}
	}

	result.Redacted = strings.Join(lines, "\n")
	return result, nil
}

type lineScan struct {
	Found    bool
	Reason   string
	Redacted string
}

func scanLine(line string) lineScan {
	lower := strings.ToLower(line)

	// Check keywords first.
	for _, kw := range secretKeywords {
		if strings.Contains(lower, kw) {
			return lineScan{
				Found:    true,
				Reason:   fmt.Sprintf("secret keyword '%s'", kw),
				Redacted: "[REDACTED: potential secret]",
			}
		}
	}

	// Check regex patterns.
	for _, re := range secretPatterns {
		if re.MatchString(line) {
			match := re.FindString(line)
			return lineScan{
				Found:    true,
				Reason:   fmt.Sprintf("secret pattern '%s...'", match[:min(len(match), 8)]),
				Redacted: "[REDACTED: potential secret]",
			}
		}
	}

	return lineScan{Found: false, Redacted: line}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
