package skillmanager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func deploymentFixture(t *testing.T) (SkillDeployment, ObjectOpener) {
	t.Helper()
	data, err := os.ReadFile("testdata/deployment-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var input SkillDeployment
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	return input, objectSource(map[string][]byte{input.Manifest.Entries[1].SHA256: []byte("---\nname: learning\ndescription: example\n---\n")})
}

func fenceDeployment(t *testing.T, store *os.Root, input SkillDeployment) {
	t.Helper()
	_, err := CloseAccountImports(store, AccountFence{Version: 1, NodeID: input.NodeID, UserID: input.UserID, AccountID: input.AccountID, DirectoryEpoch: 1})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeploymentPreparationRetainsCompletePrivateInputAndReplays(t *testing.T) {
	store, path := privateStore(t)
	input, open := deploymentFixture(t)
	fenceDeployment(t, store, input)
	first, err := PrepareDeployment(context.Background(), store, input, open, DefaultCopyPolicy())
	if err != nil || first.Validate(input) != nil {
		t.Fatal("original preparation failed", err)
	}
	never := func(context.Context, string) (io.ReadCloser, error) {
		t.Error("retained preparation redownloaded content")
		return nil, errors.New("unexpected object request")
	}
	replayed, err := PrepareDeployment(context.Background(), store, input, never, DefaultCopyPolicy())
	if err != nil || replayed != first {
		t.Fatal("exact replay changed the receipt", err)
	}
	info, err := os.Stat(filepath.Join(path, deploymentBundleName(input.AttemptID), "deployment.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("receipt is not private", err)
	}
	for name, mutate := range map[string]func(*SkillDeployment){
		"task":       func(d *SkillDeployment) { d.TaskID = d.AccountID },
		"epoch":      func(d *SkillDeployment) { d.DirectoryEpoch++ },
		"member":     func(d *SkillDeployment) { d.Items[0].StateEpoch++ },
		"generation": func(d *SkillDeployment) { d.Plan.Generation++; d.PlanDigest, _ = d.Plan.Digest() },
		"checkpoint": func(d *SkillDeployment) { d.CheckpointID = d.AccountID },
	} {
		t.Run(name, func(t *testing.T) {
			changed, _ := deploymentFixture(t)
			mutate(&changed)
			if _, err := PrepareDeployment(context.Background(), store, changed, never, DefaultCopyPolicy()); err == nil {
				t.Fatal("changed input reused the original attempt")
			}
		})
	}
}

func TestDeploymentPreparationNeverRepairsCorruptPublishedState(t *testing.T) {
	for _, kind := range []string{"bytes", "missing_file", "extra_file", "mode", "hardlink", "work_link", "record_link", "missing_record", "baseline", "ownership"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "ownership" && os.Geteuid() != 0 {
				t.Skip("requires chown authority")
			}
			store, path := privateStore(t)
			input, open := deploymentFixture(t)
			input.Manifest.Entries[1].Mode = 0o444
			input.TreeDigest, _ = Digest(input.Manifest)
			fenceDeployment(t, store, input)
			if _, err := PrepareDeployment(context.Background(), store, input, open, DefaultCopyPolicy()); err != nil {
				t.Fatal(err)
			}
			bundle := filepath.Join(path, deploymentBundleName(input.AttemptID))
			file := filepath.Join(bundle, "work/learning/SKILL.md")
			var err error
			switch kind {
			case "bytes":
				err = os.WriteFile(file, []byte("corrupt"), 0o600)
			case "missing_file":
				err = os.Remove(file)
			case "extra_file":
				err = os.WriteFile(filepath.Join(bundle, "work/extra"), []byte("unbound"), 0o600)
			case "mode":
				err = os.Chmod(file, 0o444)
			case "hardlink":
				err = os.Link(file, filepath.Join(path, "external"))
			case "ownership":
				err = os.Chown(file, 12345, 12345)
			case "work_link":
				err = os.Rename(filepath.Join(bundle, "work"), filepath.Join(bundle, "original"))
				if err == nil {
					err = os.Symlink("original", filepath.Join(bundle, "work"))
				}
			case "record_link":
				err = os.Rename(filepath.Join(bundle, "deployment.json"), filepath.Join(bundle, "original.json"))
				if err == nil {
					err = os.Symlink("original.json", filepath.Join(bundle, "deployment.json"))
				}
			case "missing_record":
				err = os.Remove(filepath.Join(bundle, "deployment.json"))
			case "baseline":
				err = os.WriteFile(filepath.Join(bundle, "baseline.json"), []byte(`{}`), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			never := func(context.Context, string) (io.ReadCloser, error) {
				t.Error("corruption triggered download repair")
				return nil, errors.New("unexpected download")
			}
			if _, err := PrepareDeployment(context.Background(), store, input, never, DefaultCopyPolicy()); err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatal("corruption was accepted or treated as absence", err)
			}
			if _, err := os.Lstat(bundle); err != nil {
				t.Fatal("corrupt evidence was removed", err)
			}
		})
	}
}

func TestDeploymentPreparationRequiresFenceAndPreservesFailedInputs(t *testing.T) {
	for _, kind := range []string{"no_fence", "foreign_fence", "newer_fence", "cancel", "source", "quota", "reserve", "runtime_link", "reserved"} {
		t.Run(kind, func(t *testing.T) {
			store, _ := privateStore(t)
			input, open := deploymentFixture(t)
			if kind != "no_fence" {
				fence := input
				if kind == "foreign_fence" {
					fence.UserID = input.NodeID
				}
				fenceDeployment(t, store, fence)
			}
			policy := DefaultCopyPolicy()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "newer_fence":
				if err := store.Remove("account-" + input.AccountID + ".json"); err != nil {
					t.Fatal(err)
				}
				_, err := CloseAccountImports(store, AccountFence{Version: 1, NodeID: input.NodeID, UserID: input.UserID, AccountID: input.AccountID, DirectoryEpoch: input.DirectoryEpoch + 1})
				if err != nil {
					t.Fatal(err)
				}
			case "cancel":
				original := open
				open = func(ctx context.Context, digest string) (io.ReadCloser, error) {
					cancel()
					return original(ctx, digest)
				}
			case "source":
				open = func(context.Context, string) (io.ReadCloser, error) { return nil, errors.New("source unavailable") }
			case "quota":
				policy.DirectoryBytes = 1
			case "reserve":
				policy.MinimumFreeBytes = math.MaxUint64
			case "runtime_link":
				input.Manifest.Entries = append(input.Manifest.Entries, Entry{Path: "runtime", Kind: "runtime_link", Mode: 0o777, Target: "/opt/tool", Dependency: "tool"})
			case "reserved":
				input.Manifest.Entries = append(input.Manifest.Entries, Entry{Path: "ego-browser", Kind: "directory", Mode: 0o700})
			}
			input.TreeDigest, _ = Digest(input.Manifest)
			if _, err := PrepareDeployment(ctx, store, input, open, policy); err == nil {
				t.Fatal("invalid preparation succeeded")
			}
			entries, err := store.Open(".")
			if err != nil {
				t.Fatal(err)
			}
			names, err := entries.Readdirnames(-1)
			_ = entries.Close()
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range names {
				if name != "account-"+input.AccountID+".json" {
					t.Fatal("failed preparation retained staging or published content", name)
				}
			}
		})
	}
}
