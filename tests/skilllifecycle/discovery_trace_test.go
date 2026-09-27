package skilllifecycle

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const (
	claudeTraceBytes     = 8 << 20
	claudeTraceLineBytes = 1 << 20
	claudeTraceEntries   = 10000
)

type claudeTraceEntry struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Message   struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type claudeTraceBlock struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input struct {
		Skill string `json:"skill"`
	} `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   json.RawMessage `json:"is_error"`
}

func verifyClaudeDiscovery(reader io.Reader, runID string) error {
	// Never propagate decoding errors: transcripts can contain credentials and private model output.
	invalid := errors.New("Claude discovery evidence is missing or invalid")
	data, err := io.ReadAll(io.LimitReader(reader, claudeTraceBytes+1))
	if err != nil || len(data) == 0 || len(data) > claudeTraceBytes || data[len(data)-1] != '\n' {
		return invalid
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), claudeTraceLineBytes)
	calls := make(map[string]bool)
	results := make(map[string]bool)
	found := false
	entries := 0
	for scanner.Scan() {
		entries++
		var entry claudeTraceEntry
		if entries > claudeTraceEntries || json.Unmarshal(scanner.Bytes(), &entry) != nil || entry.Type == "" {
			return invalid
		}
		if entry.SessionID != "" && entry.SessionID != runID {
			return invalid
		}
		if entry.Type != "assistant" && entry.Type != "user" {
			continue
		}
		if entry.SessionID != runID || entry.Message.Role != entry.Type || len(entry.Message.Content) == 0 {
			return invalid
		}
		var blocks []claudeTraceBlock
		if json.Unmarshal(entry.Message.Content, &blocks) != nil {
			var message string
			if json.Unmarshal(entry.Message.Content, &message) != nil {
				return invalid
			}
			continue
		}
		for _, block := range blocks {
			switch block.Type {
			case "tool_use":
				if entry.Type != "assistant" || block.ID == "" {
					return invalid
				}
				if _, exists := calls[block.ID]; exists {
					return invalid
				}
				calls[block.ID] = block.Name == "Skill" && block.Input.Skill == "learning"
			case "tool_result":
				if entry.Type != "user" || block.ToolUseID == "" || results[block.ToolUseID] {
					return invalid
				}
				target, exists := calls[block.ToolUseID]
				if !exists {
					return invalid
				}
				results[block.ToolUseID] = true
				if len(block.IsError) != 0 && string(block.IsError) != "false" && string(block.IsError) != "true" {
					return invalid
				}
				if target && string(block.IsError) != "true" {
					found = true
				}
			}
		}
	}
	if scanner.Err() != nil || !found || len(calls) != len(results) {
		return invalid
	}
	return nil
}
