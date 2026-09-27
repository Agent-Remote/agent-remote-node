package api

import (
	"context"
	"errors"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// RenewNodeExport exchanges a still-live credential without changing original read authority.
// The caller must keep the successor memory-only and preserve its existing authorization window.
func (c Client) RenewNodeExport(ctx context.Context, nodeID, snapshotID, deviceID, keyID, grant string) (skillmanager.NodeExportRenewal, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := c.newNodeExportRequest(ctx, nodeID, snapshotID, deviceID, keyID, grant, "renew")
	if err != nil {
		return skillmanager.NodeExportRenewal{}, err
	}
	var response nodeExportRenewalEnvelope
	if err := c.sendSkillRequest(req, &response, 16<<10); err != nil {
		return skillmanager.NodeExportRenewal{}, err
	}
	result := response.Data
	permission := result.Permission
	if response.SchemaVersion != 1 || response.Status != "authorized" || response.Committed == nil || *response.Committed || len(response.Errors) != 0 || result.MatchPrevious(grant) != nil ||
		permission.Binding.NodeID != nodeID || permission.Binding.SnapshotID != snapshotID || permission.DeviceID != deviceID || permission.SSHKeyID != keyID {
		return skillmanager.NodeExportRenewal{}, errors.New("export continuation changed request identity")
	}
	return result, nil
}

type nodeExportRenewalEnvelope skillEnvelope[skillmanager.NodeExportRenewal]

func (e *nodeExportRenewalEnvelope) UnmarshalJSON(data []byte) error {
	type plain nodeExportRenewalEnvelope
	return decodeNodeExportEnvelope(data, (*plain)(e))
}
