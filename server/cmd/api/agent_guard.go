package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/llm"
)

const maxToolArguments = 16 << 10

// validateAgentCall is an execution boundary, not a prompt suggestion. Only the tools actually
// advertised this turn are callable. Role and write-policy checks still happen inside execution.
func validateAgentCall(ctx context.Context, tc llm.ToolCall, offered map[string]bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tc.Type != "" && tc.Type != "function" {
		return errors.New("unsupported tool call type")
	}
	if !offered[tc.Func.Name] {
		return errors.New("tool was not offered for this turn")
	}
	if len(tc.ID) > 200 || strings.ContainsAny(tc.ID, "\r\n") {
		return errors.New("invalid tool call id")
	}
	if len(tc.Func.Arguments) > maxToolArguments {
		return errors.New("tool arguments exceed 16 KiB")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(tc.Func.Arguments), &obj); err != nil || obj == nil {
		return errors.New("tool arguments must be a JSON object")
	}
	return nil
}

// canonicalCallSignature treats whitespace and key ordering as the same repeated call.
func canonicalCallSignature(tc llm.ToolCall) string {
	var obj any
	if json.Unmarshal([]byte(tc.Func.Arguments), &obj) == nil {
		b, err := json.Marshal(obj)
		if err == nil {
			return tc.Func.Name + "|" + string(b)
		}
	}
	return tc.Func.Name + "|" + tc.Func.Arguments
}
