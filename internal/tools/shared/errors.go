// Package shared holds cross-tool helpers that don't belong to any single
// tool: the coded error envelope used across the tool layer, and the
// built-in tool catalog (OpenAI function schemas + canonical examples).
//
// Nothing here may depend on App state, ConfigState, or any *App receiver.
package shared

import (
	"errors"
	"fmt"
)

// CodedError is a tool error paired with a stable code.
type CodedError struct {
	code string
	err  error
}

// New returns a CodedError wrapping err with the given code. Returns nil if
// err is nil so callers can write `return shared.New(code, err)` without
// guarding.
func New(code string, err error) error {
	if err == nil {
		return nil
	}
	return CodedError{code: code, err: err}
}

// Newf is a convenience that formats an error message and wraps it with code.
func Newf(code string, format string, args ...any) error {
	return CodedError{code: code, err: fmt.Errorf(format, args...)}
}

// Error implements error.
func (e CodedError) Error() string {
	if e.code == "" {
		return e.err.Error()
	}
	return "[" + e.code + "] " + e.err.Error()
}

// Unwrap returns the underlying error so errors.Is/As traverse it.
func (e CodedError) Unwrap() error {
	return e.err
}

// ToolErrorCode returns the stable code, used by the tool result envelope.
func (e CodedError) ToolErrorCode() string {
	return e.code
}

// Code extracts the code from a CodedError chain. Returns "" if err is nil
// or no CodedError is present.
func Code(err error) string {
	var coded interface{ ToolErrorCode() string }
	if errors.As(err, &coded) {
		return coded.ToolErrorCode()
	}
	return ""
}
