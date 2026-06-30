package opencode

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// CLIExecutor runs any CLI tool by substituting {prompt} in a configurable
// argument template. If {prompt} does not appear in the template the prompt is
// appended as the last argument.
//
// Example:
//
//	CLIExecutor{
//	    Binary:      "opencode",
//	    ArgTemplate: []string{"run", "--dangerously-skip-permissions", "{prompt}"},
//	}
type CLIExecutor struct {
	// Binary is the executable to run. Must be an absolute path or a name
	// resolvable via $PATH.
	Binary string

	// ArgTemplate is the list of arguments passed to Binary.
	// {prompt} is substituted at call time with the prompt text.
	// If {prompt} is absent the prompt is appended as the final argument.
	ArgTemplate []string
}

// ParseArgTemplate splits a comma-separated template string into a slice,
// making it easy to pass the template from a CLI flag.
//
//	ParseArgTemplate("run,--dangerously-skip-permissions,{prompt}")
//	// → ["run", "--dangerously-skip-permissions", "{prompt}"]
func ParseArgTemplate(csv string) []string {
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// Execute substitutes {prompt} into ArgTemplate (or appends it) and runs Binary.
func (e *CLIExecutor) Execute(ctx context.Context, prompt string) (*Result, error) {
	hasToken := false
	args := make([]string, len(e.ArgTemplate))
	for i, arg := range e.ArgTemplate {
		if strings.Contains(arg, "{prompt}") {
			hasToken = true
		}
		args[i] = strings.ReplaceAll(arg, "{prompt}", prompt)
	}
	if !hasToken {
		args = append(args, prompt)
	}

	cmd := exec.CommandContext(ctx, e.Binary, args...) //nolint:gosec // Binary is operator-controlled
	log.Printf("executor: running command: %s %s", e.Binary, strings.Join(args, " "))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("exec %s: %w", e.Binary, err)
		}
	}

	return &Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Response: strings.TrimSpace(stdout.String()),
	}, nil
}

// OpenCodeArgTemplate is the default arg template for the OpenCode CLI.
// --dangerously-skip-permissions auto-approves permissions so the process
// never blocks waiting for interactive input.
var OpenCodeArgTemplate = []string{
	"run", "--dangerously-skip-permissions", "{prompt}",
}
