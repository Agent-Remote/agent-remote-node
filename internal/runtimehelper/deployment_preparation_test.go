package runtimehelper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func preparationTestDeployment(t *testing.T, content []byte) skillmanager.SkillDeployment {
	t.Helper()
	snapshot := preparationTestSnapshot(t, content)
	entry := snapshot.Manifest.Entries[0]
	entry.Path = "learning/SKILL.md"
	manifest := skillmanager.Manifest{Version: 1, Entries: []skillmanager.Entry{{Path: "learning", Kind: "directory", Mode: 0o555}, entry}}
	tree, err := skillmanager.Digest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	epoch := int64(9007199254740997)
	plan := skillmanager.SkillDeploymentPlan{Version: 1, UserID: snapshot.UserID, OperationID: snapshot.SessionID,
		Generation: snapshot.LibraryGeneration, AccountID: snapshot.AccountID, NodeID: snapshot.NodeID, ToolType: "claude", RuntimeBackend: "native",
		Sources: []skillmanager.SkillDeploymentSource{{Origin: "library", SourceID: snapshot.UserID, RevisionID: snapshot.SnapshotID,
			ContentDigest: tree, Name: "learning", Enabled: true, InstallationEpoch: &epoch}}}
	digest, err := plan.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return skillmanager.SkillDeployment{
		SkillDeploymentIdentity: skillmanager.SkillDeploymentIdentity{OperationID: plan.OperationID, AttemptID: snapshot.SnapshotID,
			TaskID: snapshot.TaskID, UserID: snapshot.UserID, AccountID: snapshot.AccountID, NodeID: snapshot.NodeID,
			CheckpointID: snapshot.UserID, PlanDigest: digest, TreeDigest: tree, RuntimeBackend: "native"},
		DirectoryEpoch: snapshot.DirectoryEpoch, Plan: plan, Manifest: manifest,
		Items: []skillmanager.SkillDeploymentMember{{EntryName: "learning", StateID: snapshot.AccountID, StateEpoch: epoch, CheckpointID: snapshot.TaskID}},
	}
}

func deploymentTestReceipt(t *testing.T, input skillmanager.SkillDeployment) skillmanager.DeploymentPreparation {
	t.Helper()
	digest, err := input.InputDigest()
	if err != nil {
		t.Fatal(err)
	}
	return skillmanager.DeploymentPreparation{Version: 1, Binding: input.SkillDeploymentIdentity,
		InputDigest: digest, DirectoryEpoch: input.DirectoryEpoch, Generation: input.Plan.Generation, HelperReceiptID: input.NodeID}
}

func readDeploymentTestInput(t *testing.T, connection net.Conn) (skillmanager.SkillDeployment, *bufio.Reader) {
	t.Helper()
	reader := bufio.NewReader(connection)
	line, err := readBoundedLine(reader, maxHelperRequestBytes)
	if err != nil {
		t.Error(err)
		return skillmanager.SkillDeployment{}, reader
	}
	var request Request
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	if err := decoder.Decode(&request); err != nil {
		t.Error(err)
	}
	header, err := validateDeploymentPreparationRequest(request)
	if err != nil {
		t.Error(err)
	}
	_ = json.NewEncoder(connection).Encode(deploymentPreparationFrame{Version: 1, Kind: "input"})
	data := make([]byte, header.InputSize)
	if _, err := io.ReadFull(reader, data); err != nil {
		t.Error(err)
	}
	var input skillmanager.SkillDeployment
	if err := json.Unmarshal(data, &input); err != nil || input.AttemptID != header.AttemptID || input.TaskID != header.TaskID {
		t.Error("original deployment header/input mismatch", err)
	}
	return input, reader
}

func TestDeploymentPreparationProtocolStreamsOriginalObjectsAndReceipt(t *testing.T) {
	content := []byte("original skill instructions")
	input := preparationTestDeployment(t, content)
	expected := deploymentTestReceipt(t, input)
	client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
		received, reader := readDeploymentTestInput(t, connection)
		if received.Plan.Generation != input.Plan.Generation {
			t.Error("original generation lost precision")
		}
		_ = json.NewEncoder(connection).Encode(deploymentPreparationFrame{Version: 1, Kind: "object", Digest: input.Manifest.Entries[1].SHA256})
		data := make([]byte, len(content)+1)
		if _, err := io.ReadFull(reader, data); err != nil || !bytes.Equal(data[:len(content)], content) || data[len(content)] != skillObjectComplete {
			t.Error("object stream or completion marker changed", err)
		}
		_ = json.NewEncoder(connection).Encode(deploymentPreparationFrame{Version: 1, Kind: "prepared", Receipt: &expected})
	})
	receipt, err := client.PrepareSkillDeployment(context.Background(), "deployment_original", input, func(_ context.Context, _ skillmanager.Entry, writer io.Writer) error {
		_, err := writer.Write(content)
		return err
	})
	if err != nil || receipt != expected {
		t.Fatal("complete preparation did not retain its identity", err)
	}
}

func TestDeploymentPreparationProtocolRejectsForeignAndMalformedReceipts(t *testing.T) {
	input := preparationTestDeployment(t, []byte("content"))
	for _, kind := range []string{"task", "input", "epoch", "generation", "receipt", "null", "alias", "duplicate", "object", "extra", "version"} {
		t.Run(kind, func(t *testing.T) {
			receipt := deploymentTestReceipt(t, input)
			switch kind {
			case "task":
				receipt.Binding.TaskID = input.NodeID
			case "input":
				receipt.InputDigest = input.TreeDigest
			case "epoch":
				receipt.DirectoryEpoch++
			case "generation":
				receipt.Generation++
			case "receipt":
				receipt.HelperReceiptID = "invalid"
			}
			frame := deploymentPreparationFrame{Version: 1, Kind: "prepared", Receipt: &receipt}
			if kind == "object" {
				frame.Kind, frame.Digest, frame.Receipt = "object", input.PlanDigest, nil
			}
			if kind == "version" {
				frame.Version = 2
			}
			data, _ := json.Marshal(frame)
			switch kind {
			case "null":
				data = []byte(`{"version":1,"kind":"prepared","receipt":null}`)
			case "alias":
				data = bytes.Replace(data, []byte(`"task_id"`), []byte(`"TASK_ID"`), 1)
			case "duplicate":
				data = bytes.Replace(data, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
			case "extra":
				data = bytes.Replace(data, []byte(`"kind":"prepared"`), []byte(`"kind":"prepared","host_path":"/tmp"`), 1)
			}
			client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
				_, _ = readDeploymentTestInput(t, connection)
				_, _ = connection.Write(append(data, '\n'))
			})
			if _, err := client.PrepareSkillDeployment(context.Background(), "deployment_original", input, func(context.Context, skillmanager.Entry, io.Writer) error {
				t.Error("unbound object requested")
				return errors.New("unexpected download")
			}); err == nil {
				t.Fatal("invalid Helper authority accepted")
			}
		})
	}
}

func TestDeploymentPreparationHeaderRejectsAmbiguousAuthority(t *testing.T) {
	for _, payload := range []string{
		`{"attempt_id":"55555555-5555-4555-8555-555555555555","task_id":"66666666-6666-4666-8666-666666666666","input_size":1.0}`,
		`{"attempt_id":"55555555-5555-4555-8555-555555555555","task_id":"66666666-6666-4666-8666-666666666666","input_size":true}`,
		`{"attempt_id":"55555555-5555-4555-8555-555555555555","task_id":"66666666-6666-4666-8666-666666666666","input_size":67108865}`,
		`{"attempt_id":"../escape","task_id":"66666666-6666-4666-8666-666666666666","input_size":10}`,
		`{"attempt_id":"55555555-5555-4555-8555-555555555555","task_id":"66666666-6666-4666-8666-666666666666","input_size":0}`,
		`{"attempt_id":"55555555-5555-4555-8555-555555555555","task_id":"66666666-6666-4666-8666-666666666666","input_size":10,"path":"/tmp"}`,
	} {
		var fields map[string]any
		decoder := json.NewDecoder(bytes.NewBufferString(payload))
		decoder.UseNumber()
		if err := decoder.Decode(&fields); err != nil {
			t.Fatal(err)
		}
		if _, err := validateDeploymentPreparationRequest(Request{Version: 1, RequestID: "test", Operation: deploymentPreparationOperation, Payload: fields}); err == nil {
			t.Fatal("invalid preparation header accepted")
		}
	}
}
