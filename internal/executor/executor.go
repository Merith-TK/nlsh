// Package executor runs shell commands and streams their output.
package executor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RunChained joins commands with && and runs them as a single shell invocation.
func RunChained(commands []string) error {
	joined := strings.Join(commands, " && ")
	return runShell(joined)
}

// RunSequential runs each command one at a time, printing output between each.
func RunSequential(commands []string) error {
	for i, cmd := range commands {
		if i > 0 {
			fmt.Println()
		}
		if err := runShell(cmd); err != nil {
			return fmt.Errorf("command %d failed: %w", i+1, err)
		}
	}
	return nil
}

func runShell(cmd string) error {
	c := exec.Command("sh", "-c", cmd)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
