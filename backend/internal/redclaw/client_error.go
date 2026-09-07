package redclaw

import (
	"encoding/json"
	"fmt"
)

// decodeRedClawError maps an upstream error body. Platform-gateway uses
// nested {"error":{"code","message"}}; the legacy pocket adapter used
// top-level {"code":int,"message"}. Empty top-level unmarshal must not
// win — that produced "RedClaw error (code=0): ".
func decodeRedClawError(status int, body []byte) error {
	var nested struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &nested) == nil && nested.Error.Message != "" {
		return fmt.Errorf("RedClaw HTTP %d (%s): %s", status, nested.Error.Code, nested.Error.Message)
	}
	var flat ErrorResponse
	if json.Unmarshal(body, &flat) == nil && (flat.Code != 0 || flat.Message != "") {
		return fmt.Errorf("RedClaw error (code=%d): %s", flat.Code, flat.Message)
	}
	return fmt.Errorf("RedClaw HTTP %d: %s", status, string(body))
}
