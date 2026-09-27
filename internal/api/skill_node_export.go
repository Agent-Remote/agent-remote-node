package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// VerifyNodeExport reauthorizes the original user grant and exact forced-command device/key.
// It cannot renew a grant, upload content, acknowledge durability or invoke the Helper.
func (c Client) VerifyNodeExport(ctx context.Context, nodeID, snapshotID, deviceID, keyID, grant string) (skillmanager.NodeExportPermission, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := c.newNodeExportRequest(ctx, nodeID, snapshotID, deviceID, keyID, grant, "verify")
	if err != nil {
		return skillmanager.NodeExportPermission{}, err
	}
	var response nodeExportEnvelope
	if err := c.sendSkillRequest(req, &response, 16<<10); err != nil {
		return skillmanager.NodeExportPermission{}, err
	}
	permission := response.Data
	if response.SchemaVersion != 1 || response.Status != "authorized" || response.Committed == nil || *response.Committed || len(response.Errors) != 0 ||
		permission.Validate() != nil || permission.Binding.NodeID != nodeID || permission.Binding.SnapshotID != snapshotID || permission.DeviceID != deviceID || permission.SSHKeyID != keyID {
		return skillmanager.NodeExportPermission{}, errors.New("frozen export permission changed request identity")
	}
	return permission, nil
}

func (c Client) newNodeExportRequest(ctx context.Context, nodeID, snapshotID, deviceID, keyID, grant, operation string) (*http.Request, error) {
	for _, id := range []string{nodeID, snapshotID, deviceID, keyID} {
		if !validSkillUUID(id) {
			return nil, errors.New("invalid frozen export request")
		}
	}
	if !skillmanager.ValidNodeExportGrantShape(grant) || operation != "verify" && operation != "renew" {
		return nil, errors.New("invalid frozen export grant")
	}
	request := struct {
		DeviceID string `json:"device_id"`
		SSHKeyID string `json:"ssh_key_id"`
		Grant    string `json:"grant"`
	}{deviceID, keyID, grant}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	req, err := c.newSkillRequest(ctx, http.MethodPost, "/api/v1/node/skill-state-exports/"+snapshotID+"/"+operation, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// Periodic authorization can coincide with the server's HTTP/1 idle expiry.
	// Do not retain this connection for renewal, or replay an uncertain failed POST.
	req.Close = true
	return req, nil
}

type nodeExportEnvelope skillEnvelope[skillmanager.NodeExportPermission]

func (e *nodeExportEnvelope) UnmarshalJSON(data []byte) error {
	type plain nodeExportEnvelope
	return decodeNodeExportEnvelope(data, (*plain)(e))
}

func decodeNodeExportEnvelope(data []byte, target any) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range []string{"schema_version", "status", "committed", "data", "errors"} {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return errors.New("missing frozen export envelope field")
		}
	}
	for name, value := range fields {
		switch name {
		case "schema_version", "status", "committed", "data", "errors":
		case "operation_id":
			if !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return errors.New("unexpected frozen export operation")
			}
		case "retryable":
			if !bytes.Equal(bytes.TrimSpace(value), []byte("false")) {
				return errors.New("unexpected frozen export retry authority")
			}
		default:
			return errors.New("unknown frozen export envelope field")
		}
	}
	return json.Unmarshal(data, target)
}
