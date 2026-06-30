// Package opencode wraps AI CLI tools as pluggable Executors.
package opencode

import (
	"context"
)

// Result holds the output of a single executor invocation.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Response string // trimmed stdout, used as the canonical response
}

// Executor is the interface for running prompts against an AI backend.
type Executor interface {
	Execute(ctx context.Context, prompt string) (*Result, error)
}

// OpenCodeExecutor invokes the OpenCode CLI binary using the default arg template.
// It is a convenience wrapper around CLIExecutor.
type OpenCodeExecutor struct {
	*CLIExecutor
}

// NewExecutor returns an OpenCodeExecutor configured for the given binary.
// Pass an empty binaryPath to use "opencode" from $PATH.
func NewExecutor(binaryPath string) *OpenCodeExecutor {
	if binaryPath == "" {
		binaryPath = "opencode"
	}
	return &OpenCodeExecutor{
		CLIExecutor: &CLIExecutor{
			Binary:      binaryPath,
			ArgTemplate: OpenCodeArgTemplate,
		},
	}
}
