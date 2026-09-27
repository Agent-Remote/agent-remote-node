package runtimehelper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestHelperDefaultCapacityReconciliation(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_CAPACITY_TEST") != "1" {
		t.Skip("requires disposable Linux storage and actual default-limit capture")
	}
	if _, err := exec.LookPath("setfacl"); err != nil || os.Geteuid() != 0 {
		t.Fatal("explicit capacity acceptance requires root and real Linux ACL tools")
	}
	engine, input, spec, launch := preparedManagedLaunch(t)
	sealReconciliationLaunch(t, engine, launch)
	engine.config.SystemctlPath, _ = reconciliationSystemctl(t, spec, "clean", strings.Repeat("a", 32))
	bundle, receipt, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	for index := 1; index < skillmanager.DefaultStatePolicy().Entries; index++ {
		file, err := bundle.OpenFile(fmt.Sprintf("work/state-%06d", index), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := fmt.Fprintf(file, "unique runtime object %06d\n", index)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal("cannot create capacity fixture", writeErr, closeErr)
		}
	}
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, func(ctx context.Context, connection net.Conn) {
		// This fixture must not impose its ordinary 45-second cutoff on the production handler.
		_ = connection.SetDeadline(time.Now().Add(20 * time.Minute))
		server.handle(ctx, connection)
	})
	started := time.Now()
	observed, err := client.ReconcileSkillSession(context.Background(), "capacity-reconcile", input.Snapshot.NodeID, spec.SessionID)
	if err != nil || observed.Record == nil || observed.State != "finalized" {
		t.Fatal("default-limit capture did not complete through the Helper socket", err)
	}
	if observed.Record.Unclean || observed.Record.State != "local_durable" {
		t.Fatal("capacity capture changed termination classification or claimed remote saving")
	}
	t.Logf("socket_capture_elapsed=%s entries=%d", time.Since(started).Round(time.Millisecond), skillmanager.DefaultStatePolicy().Entries)
	file, record, err := skillmanager.OpenFinalizationManifest(bundle, receipt.Snapshot.Binding)
	if err != nil || record != *observed.Record {
		t.Fatal("socket result differs from durable capacity capture", err)
	}
	defer file.Close()
	var manifest skillmanager.Manifest
	if err := json.NewDecoder(file).Decode(&manifest); err != nil || len(manifest.Entries) != skillmanager.DefaultStatePolicy().Entries {
		t.Fatal("capacity manifest is incomplete", err)
	}
	for index, entry := range manifest.Entries {
		content, name := "original", "notes"
		if index > 0 {
			content, name = fmt.Sprintf("unique runtime object %06d\n", index), fmt.Sprintf("state-%06d", index)
		}
		hash := sha256.Sum256([]byte(content))
		if entry.Path != name || entry.Size != int64(len(content)) || entry.SHA256 != hex.EncodeToString(hash[:]) {
			t.Fatal("capacity socket capture omitted or changed runtime content")
		}
	}
	started = time.Now()
	objects, err := client.OpenSkillFinalizationObjects(context.Background(), "capacity-objects", record)
	if err != nil {
		t.Fatal(err)
	}
	defer objects.Close()
	for _, expected := range manifest.Entries {
		object, actual, err := objects.Open(context.Background(), expected.SHA256)
		if err != nil || actual != expected {
			t.Fatal("capacity reader changed object metadata", err)
		}
		content, err := io.ReadAll(io.LimitReader(object, expected.Size+1))
		_ = object.Close()
		if err != nil || skillmanager.VerifyContent(expected, content) != nil {
			t.Fatal("capacity descriptor changed content", err)
		}
	}
	t.Logf("socket_objects_elapsed=%s distinct_objects=%d", time.Since(started).Round(time.Millisecond), len(manifest.Entries))

}
