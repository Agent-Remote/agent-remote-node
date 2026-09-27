package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
)

var nodeExportGrantPattern = regexp.MustCompile(`^[A-Za-z0-9_=-]+\.[0-9a-f]{64}$`)

// ValidNodeExportGrantShape checks only transport shape, never signature or live authority.
func ValidNodeExportGrantShape(grant string) bool {
	return len(grant) <= 4096 && nodeExportGrantPattern.MatchString(grant)
}

// NodeExportRenewal carries an ephemeral successor credential and the exact current permission.
// It must never be stored in a journal, exported bundle or log.
type NodeExportRenewal struct {
	PreviousGrantDigest string               `json:"previous_grant_digest"`
	Grant               string               `json:"grant"`
	Permission          NodeExportPermission `json:"permission"`
}

// MatchPrevious binds the response to the exact credential submitted on this request.
func (r NodeExportRenewal) MatchPrevious(previous string) error {
	if !ValidNodeExportGrantShape(previous) || !ValidNodeExportGrantShape(r.Grant) || r.Permission.Validate() != nil {
		return errors.New("invalid export continuation")
	}
	digest := sha256.Sum256([]byte(previous))
	if r.PreviousGrantDigest != hex.EncodeToString(digest[:]) {
		return errors.New("export continuation changed predecessor")
	}
	return nil
}

// UnmarshalJSON rejects aliases, extra fields and missing metadata before credential use.
func (r *NodeExportRenewal) UnmarshalJSON(data []byte) error {
	type plain NodeExportRenewal
	var value plain
	if err := decodeFinalizationFields(data, &value, []string{"previous_grant_digest", "grant", "permission"}, nil); err != nil {
		return err
	}
	if !contentDigestPattern.MatchString(value.PreviousGrantDigest) || !ValidNodeExportGrantShape(value.Grant) || value.Permission.Validate() != nil {
		return errors.New("invalid export continuation")
	}
	*r = NodeExportRenewal(value)
	return nil
}
