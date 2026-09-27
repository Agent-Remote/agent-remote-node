package skillmanager

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
)

// SkillDeploymentPlan preserves all original sources, including disabled selections.
type SkillDeploymentPlan struct {
	Version        int                     `json:"version"`
	UserID         string                  `json:"user_id"`
	OperationID    string                  `json:"operation_id"`
	Generation     int64                   `json:"generation"`
	AccountID      string                  `json:"account_id"`
	NodeID         string                  `json:"node_id"`
	ToolType       string                  `json:"tool_type"`
	RuntimeBackend string                  `json:"runtime_backend"`
	Sources        []SkillDeploymentSource `json:"sources"`
}

// SkillDeploymentSource distinguishes a pinned library epoch from an account-local original.
type SkillDeploymentSource struct {
	Origin            string `json:"origin"`
	SourceID          string `json:"source_id"`
	RevisionID        string `json:"revision_id"`
	ContentDigest     string `json:"content_digest"`
	Name              string `json:"name"`
	Enabled           bool   `json:"enabled"`
	InstallationEpoch *int64 `json:"installation_epoch,omitempty"`
}

// Digest matches the Server's canonical plan digest without converting integers to floats.
func (p SkillDeploymentPlan) Digest() (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	p.Sources = append([]SkillDeploymentSource{}, p.Sources...)
	slices.SortFunc(p.Sources, func(a, b SkillDeploymentSource) int {
		if order := cmp.Compare(a.Origin, b.Origin); order != 0 {
			return order
		}
		return cmp.Compare(a.SourceID, b.SourceID)
	})
	// Validated plan strings are canonical ASCII IDs, names and fixed protocol labels.
	return deploymentJSONDigest(p)
}

func (p SkillDeploymentPlan) validate() error {
	if p.Version != 1 || p.Generation < 0 || p.ToolType != "claude" || p.RuntimeBackend != "native" || p.Sources == nil {
		return errors.New("invalid deployment plan metadata")
	}
	for _, id := range []string{p.UserID, p.OperationID, p.AccountID, p.NodeID} {
		if !validSkillUUID(id) {
			return errors.New("invalid deployment plan owner")
		}
	}
	identities, names := make(map[string]bool), make(map[string]bool)
	for _, source := range p.Sources {
		identity := source.Origin + ":" + source.SourceID
		if !validSkillUUID(source.SourceID) || !validSkillUUID(source.RevisionID) || !contentDigestPattern.MatchString(source.ContentDigest) ||
			!snapshotSkillName.MatchString(source.Name) || source.Name == "ego-browser" || source.Name == "agent-remote-device" || identities[identity] ||
			source.Enabled && names[source.Name] {
			return errors.New("invalid deployment source")
		}
		switch source.Origin {
		case "library":
			if source.InstallationEpoch == nil || *source.InstallationEpoch < 1 {
				return errors.New("missing deployment installation epoch")
			}
		case "account_local":
			if source.InstallationEpoch != nil {
				return errors.New("local deployment source has library epoch")
			}
		default:
			return errors.New("invalid deployment source origin")
		}
		identities[identity] = true
		if source.Enabled {
			names[source.Name] = true
		}
	}
	return nil
}

func deploymentJSONDigest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}
