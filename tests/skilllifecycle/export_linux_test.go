package skilllifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillexport"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestNativeSSHExport(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_TEST") != "1" {
		t.Skip("requires real CLI/HTTP/SSH and disposable production daemons")
	}
	lifetime, phase := 10*time.Minute, 2*time.Minute
	capacity := os.Getenv("AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_CAPACITY") == "1"
	bytesCapacity := os.Getenv("AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_BYTES") == "1"
	longTransfer := os.Getenv("AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_LONG") == "1"
	destinationFull := os.Getenv("AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_DESTINATION_FULL") == "1"
	if capacity || bytesCapacity || longTransfer {
		lifetime, phase = 30*time.Minute, 20*time.Minute
	}
	ctx, f := lifecycleSetupWithin(t, lifetime)
	if !f.SSHExport {
		t.Fatal("missing explicit export fixture")
	}
	if f.ExportLong != longTransfer || longTransfer && (capacity || bytesCapacity) {
		t.Fatal("long export requires a matching independent fixture opt-in")
	}
	if f.ExportDestinationFull != destinationFull || destinationFull && (!f.ExportQuota || capacity || bytesCapacity || longTransfer) {
		t.Fatal("destination exhaustion requires its own stopped-work fixture")
	}
	oversize := os.Getenv("AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_OVERSIZE") == "1"
	overEntries := os.Getenv("AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_OVER_ENTRIES") == "1"
	if f.ExportOverEntries != overEntries || overEntries && (!capacity || !f.ExportQuota || bytesCapacity || longTransfer) {
		t.Fatal("over-entry recovery requires its own stopped-work entry fixture")
	}
	if f.ExportCapacity != capacity || f.ExportBytes != bytesCapacity || f.ExportOversize != oversize || oversize && (!bytesCapacity || !f.ExportQuota) {
		t.Fatal("export capacity requires matching explicit fixture opt-in")
	}
	wait := func(description string, check func() bool) { awaitWithin(t, ctx, phase, description, check) }
	startExportSSH(t, ctx, false)
	args := []string{"--export-proof"}
	if longTransfer || destinationFull {
		args = append(args, "--export-long")
	}
	if capacity {
		args = append(args, "--export-capacity")
	}
	if bytesCapacity {
		args = append(args, "--export-bytes")
	}
	if oversize {
		args = append(args, "--export-oversize")
	}
	if overEntries {
		args = append(args, "--export-over-entries")
	}
	id := createSession(t, ctx, f, args)
	if bytesCapacity {
		runAttachedWithin(t, ctx, id, false, phase)
	} else {
		runAttached(t, ctx, id, false)
	}
	bundle := filepath.Join(skillRoot, "session-"+id)
	wait("ordinary local termination evidence", func() bool {
		_, err := os.Stat(filepath.Join(bundle, "termination.json"))
		return err == nil
	})
	if !f.ExportQuota {
		wait("ordinary local frozen finalization", func() bool {
			_, err := os.Stat(filepath.Join(bundle, "finalization/record.json"))
			return err == nil
		})
		wait("ordinary stopped observation", func() bool {
			state, err := f.request(ctx, "GET", "/api/v1/sessions/"+id, nil)
			return err == nil && state.Data.Status == "stopped"
		})
	}
	command(t, ctx, "systemctl", "stop", workerUnit, helperUnit)
	expected := exportExpectation(t, ctx, id, f.ExportQuota)
	checkExportCapacity(t, expected, capacity, bytesCapacity, oversize, overEntries)
	startDaemons(t, ctx)
	before := exportInventory(t, bundle)
	if exec.CommandContext(ctx, "runuser", "-u", "ar-proof-worker", "--", "cat", filepath.Join(bundle, "finalization/manifest.json")).Run() == nil {
		t.Fatal("SSH peer can directly traverse private Skill store")
	}
	encoded, err := json.Marshal(expected)
	if err != nil || os.WriteFile("/proof/control/expected.json", encoded, 0600) != nil {
		t.Fatal("cannot publish private test coordination identity")
	}
	t.Log("SSH_EXPORT_READY")
	wait("actual host CLI export and rejection checks", func() bool {
		_, err := os.Stat("/proof/control/done")
		return err == nil
	})
	command(t, ctx, "systemctl", "stop", workerUnit, helperUnit)
	if !reflect.DeepEqual(before, exportInventory(t, bundle)) {
		t.Fatal("read-only export changed retained work or frozen evidence")
	}
}

func checkExportCapacity(t *testing.T, header exportExpected, entryCapacity, byteCapacity, oversize, overEntries bool) {
	t.Helper()
	if entryCapacity {
		objects := 99_997
		if byteCapacity {
			objects = 99_988
		}
		count := 100_000
		if overEntries {
			count++
			objects++
		}
		if len(header.Manifest.Entries) != count || header.FileObjects != objects {
			t.Fatal("SSH capacity capture did not preserve every distinct object")
		}
	} else if byteCapacity && (len(header.Manifest.Entries) != 36 || header.FileObjects != 24) {
		t.Fatal("SSH byte capacity fixture changed its expected entries")
	}
	if byteCapacity {
		var expanded, unique int64
		seen := make(map[string]bool)
		for _, entry := range header.Manifest.Entries {
			if entry.Kind == "file" {
				expanded += entry.Size
				if !seen[entry.SHA256] {
					seen[entry.SHA256] = true
					unique += entry.Size
				}
			}
		}
		expected := int64(10 << 30)
		if oversize {
			expected += 1 << 20
		}
		if expanded != expected || unique != expanded {
			t.Fatal("SSH byte capacity must transfer its exact distinct bytes without deduplication")
		}
	}
}

// The test oracle may hold all entries; it is not a publishable complete manifest v1.
type exportExpected struct {
	Binding     skillmanager.NodeExportBinding `json:"binding"`
	TreeDigest  string                         `json:"tree_digest"`
	FileObjects int                            `json:"file_objects"`
	Manifest    struct {
		Entries []skillmanager.Entry `json:"entries"`
	} `json:"manifest"`
}

func exportExpectation(t *testing.T, ctx context.Context, id string, quota bool) exportExpected {
	t.Helper()
	store, err := os.OpenRoot(skillRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bundle, session, err := skillmanager.OpenSessionSnapshot(store, id)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	var manifest skillmanager.Manifest
	expected := exportExpected{Binding: session.Snapshot.Binding.ExportBinding()}
	if quota {
		if session.Snapshot.Capture.DirectoryBytes != 1024 {
			t.Fatal("runtime quota fixture was not applied")
		}
		if _, err := bundle.Lstat("finalization"); !os.IsNotExist(err) {
			t.Fatal("quota failure must not create a partial capture")
		}
		observation, err := skillmanager.OpenStoppedWorkRecovery(ctx, bundle, session)
		if err != nil {
			t.Fatal(err)
		}
		defer observation.Close()
		if observation.Unclean() {
			t.Fatal("natural exit classification was lost")
		}
		err = observation.Walk(ctx, func(entry skillmanager.Entry, input io.Reader) error {
			expected.Manifest.Entries = append(expected.Manifest.Entries, entry)
			if input != nil {
				_, err := io.Copy(io.Discard, input)
				return err
			}
			return nil
		})
		if err != nil || observation.Verify(ctx) != nil {
			t.Fatal("recovery oracle did not completely verify retained source", err)
		}
		expected.TreeDigest = observation.Observation().TreeDigest
		objects := map[string]bool{}
		for _, entry := range expected.Manifest.Entries {
			if entry.Kind == "file" {
				objects[entry.SHA256] = true
			}
		}
		expected.FileObjects = len(objects)
		return expected
	} else {
		data, err := bundle.ReadFile("finalization/manifest.json")
		if err != nil || json.Unmarshal(data, &manifest) != nil {
			t.Fatal("invalid original frozen manifest")
		}
	}
	b := session.Snapshot.Binding
	binding := skillmanager.NodeExportBinding{SnapshotID: b.SnapshotID, SessionID: b.SessionID, UserID: b.UserID,
		AccountID: b.AccountID, NodeID: b.NodeID, TaskID: b.TaskID, LibraryGeneration: b.LibraryGeneration,
		DirectoryEpoch: b.DirectoryEpoch, InitialTreeDigest: b.InitialTreeDigest}
	header, err := skillexport.SnapshotHeader(binding, manifest, false)
	if err != nil {
		t.Fatal(err)
	}
	expected.TreeDigest, expected.FileObjects, expected.Manifest.Entries = header.TreeDigest, header.FileObjects, header.Manifest.Entries
	return expected
}

func startExportSSH(t *testing.T, ctx context.Context, terminal bool) {
	t.Helper()
	command(t, ctx, "usermod", "--shell", "/bin/sh", "--password", "*", "ar-proof-worker")
	command(t, ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", "/proof/ssh_host_ed25519_key")
	if err := os.MkdirAll("/run/sshd", 0755); err != nil {
		t.Fatal(err)
	}
	config := "Port 2222\nListenAddress 0.0.0.0\nHostKey /proof/ssh_host_ed25519_key\nPidFile /run/proof-sshd.pid\nAuthorizedKeysFile " + workerRoot + "/authorized_keys\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nPermitRootLogin no\nAllowUsers ar-proof-worker\nDisableForwarding yes\nPermitTTY no\nLogLevel ERROR\n"
	if terminal {
		config = strings.Replace(config, "PermitTTY no", "PermitTTY yes", 1)
	}
	if err := os.WriteFile("/proof/sshd_config", []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	command(t, ctx, "/usr/sbin/sshd", "-t", "-f", "/proof/sshd_config")
	process := exec.CommandContext(ctx, "/usr/sbin/sshd", "-D", "-f", "/proof/sshd_config")
	if err := process.Start(); err != nil {
		t.Fatal("cannot start isolated SSH server")
	}
	t.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })
}

type exportFileIdentity struct {
	Mode                    uint32
	UID, GID                uint32
	Inode                   uint64
	Size, Modified, Changed int64
	Digest, Link            string
}

func exportInventory(t *testing.T, root string) map[string]exportFileIdentity {
	t.Helper()
	result := make(map[string]exportFileIdentity)
	buffer := make([]byte, 64<<10)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stat := info.Sys().(*syscall.Stat_t)
		value := exportFileIdentity{Mode: stat.Mode, UID: stat.Uid, GID: stat.Gid, Inode: stat.Ino,
			Size: info.Size(), Modified: info.ModTime().UnixNano(), Changed: stat.Ctim.Sec*1e9 + stat.Ctim.Nsec}
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			digest := sha256.New()
			_, readErr := io.CopyBuffer(digest, struct{ io.Reader }{file}, buffer)
			closeErr := file.Close()
			if readErr != nil {
				return readErr
			}
			if closeErr != nil {
				return closeErr
			}
			value.Digest = hex.EncodeToString(digest.Sum(nil))
		}
		if info.Mode()&os.ModeSymlink != 0 {
			value.Link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[name] = value
		return nil
	})
	if err != nil {
		t.Fatal("cannot inventory retained export data", err)
	}
	return result
}
