// Package fs provides filesystem exploration tools with security enforcement.
package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AccessLevel determines how a path is handled.
type AccessLevel int

const (
	AccessBlock  AccessLevel = iota // Outright refusal
	AccessFilter                    // Scan for secrets, ask user if found
	AccessAllow                     // Direct access
)

// Enforcer holds the security policy for filesystem access.
type Enforcer struct {
	WorkspaceRoot string // Current working directory, agent confined here by default
}

// NewEnforcer creates an enforcer scoped to the current working directory.
func NewEnforcer() (*Enforcer, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return &Enforcer{WorkspaceRoot: cwd}, nil
}

// CheckPath evaluates a path and returns its access level + reason.
func (e *Enforcer) CheckPath(path string) (AccessLevel, string) {
	// Resolve to absolute path.
	abs, err := filepath.Abs(path)
	if err != nil {
		return AccessBlock, fmt.Sprintf("cannot resolve path: %v", err)
	}

	// Clean the path.
	abs = filepath.Clean(abs)

	// Check if it's outside the workspace.
	rel, err := filepath.Rel(e.WorkspaceRoot, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return AccessBlock, "path is outside the current workspace"
	}

	// Check for blocked system paths and patterns.
	lower := strings.ToLower(abs)
	for _, pattern := range blockedPatterns {
		if strings.Contains(lower, pattern) {
			return AccessBlock, fmt.Sprintf("access to system path pattern '%s' is blocked", pattern)
		}
	}

	// Check file info.
	info, err := os.Stat(abs)
	if err != nil {
		return AccessBlock, fmt.Sprintf("path does not exist or is inaccessible: %v", err)
	}

	// Block directories that look like system dirs.
	if info.IsDir() {
		base := filepath.Base(abs)
		for _, dir := range blockedDirNames {
			if strings.EqualFold(base, dir) {
				return AccessBlock, fmt.Sprintf("system directory '%s' is blocked", dir)
			}
		}
	}

	// Block binary files.
	if !info.IsDir() && isBinaryFile(abs) {
		return AccessBlock, "binary files cannot be read"
	}

	// Check if file matches filter patterns (scan for secrets).
	if !info.IsDir() {
		for _, pattern := range filterPatterns {
			if matched, _ := filepath.Match(pattern, filepath.Base(abs)); matched {
				return AccessFilter, "file matches filter pattern — scanning for secrets"
			}
		}
		// Also check extensions.
		ext := strings.ToLower(filepath.Ext(abs))
		for _, fe := range filterExtensions {
			if ext == fe {
				return AccessFilter, fmt.Sprintf("file extension '%s' requires secret scan", fe)
			}
		}
	}

	return AccessAllow, ""
}

var blockedPatterns = []string{
	"/etc/shadow", "/etc/passwd", "/etc/sudoers",
	"/proc/", "/sys/", "/dev/",
	".ssh/", ".gnupg/",
}

var blockedDirNames = []string{
	".ssh", ".gnupg", "proc", "sys", "dev",
}

var filterPatterns = []string{
	"*config*", "*secret*", "*token*", "*credential*", "*private*",
	".env*", ".envrc", ".aws",
}

var filterExtensions = []string{
	".toml", ".yaml", ".yml", ".json", ".ini", ".cfg",
}

// isBinaryFile checks if a file appears to be binary by reading the first 512 bytes.
func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true // Treat unreadable as binary/block
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	buf = buf[:n]

	// Simple heuristic: if there are null bytes or a high ratio of non-printable chars, it's binary.
	if n == 0 {
		return false // Empty file is fine
	}

	nullCount := 0
	for _, b := range buf {
		if b == 0 {
			nullCount++
		}
	}
	// If more than 1 null byte in 512 bytes, consider it binary.
	if nullCount > 1 {
		return true
	}

	return false
}
