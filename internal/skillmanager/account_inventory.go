package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
)

// AccountWriter is a content-free Server inventory entry; unknown original backends remain null.
type AccountWriter struct {
	Kind           string  `json:"kind"`
	NodeID         string  `json:"node_id"`
	ResourceID     string  `json:"resource_id"`
	RuntimeBackend *string `json:"runtime_backend"`
	TaskID         *string `json:"task_id"`
}

var accountResourcePattern = regexp.MustCompile(`^[A-Za-z0-9:_-]{1,128}$`)

// AccountInventoryDigest validates bounded identity records and matches Server canonical JSON hashing.
func AccountInventoryDigest(writers []AccountWriter) (string, error) {
	if len(writers) > 10_000 {
		return "", errors.New("account writer inventory exceeds inspection limit")
	}
	canonical := make([]map[string]any, 0, len(writers))
	for _, writer := range writers {
		if validateAccountIdentity(writer.NodeID) != nil || !accountResourcePattern.MatchString(writer.ResourceID) ||
			writer.TaskID != nil && validateAccountIdentity(*writer.TaskID) != nil ||
			writer.RuntimeBackend != nil && *writer.RuntimeBackend != "native" && *writer.RuntimeBackend != "docker_sandbox" {
			return "", errors.New("invalid account writer identity")
		}
		switch writer.Kind {
		case "session", "binding":
			if validateAccountIdentity(writer.ResourceID) != nil {
				return "", errors.New("invalid account runtime resource identity")
			}
		case "import", "backend":
			if writer.TaskID == nil {
				return "", errors.New("account mutation inventory lacks task identity")
			}
		default:
			return "", errors.New("invalid account writer kind")
		}
		canonical = append(canonical, map[string]any{
			"kind": writer.Kind, "node_id": writer.NodeID, "resource_id": writer.ResourceID,
			"runtime_backend": writer.RuntimeBackend, "task_id": writer.TaskID,
		})
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
