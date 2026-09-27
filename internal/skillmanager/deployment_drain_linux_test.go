package skillmanager

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestDeploymentDrainPermanentlyFencesAbsentAndPreparedAttempts(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "absent"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			store, path := privateStore(t)
			input, open := deploymentFixture(t)
			fenceDeployment(t, store, input)
			var original DeploymentPreparation
			var err error
			if prepared {
				original, err = PrepareDeployment(context.Background(), store, input, open, DefaultCopyPolicy())
				if err != nil {
					t.Fatal(err)
				}
				foreign := input.SkillDeploymentIdentity
				foreign.TaskID = foreign.AccountID
				if _, err := DrainDeployment(context.Background(), store, foreign); err == nil {
					t.Fatal("changed original preparation identity was drained")
				}
			}
			first, err := DrainDeployment(context.Background(), store, input.SkillDeploymentIdentity)
			if err != nil || first.Validate(input.SkillDeploymentIdentity) != nil {
				t.Fatal("drain failed", err)
			}
			reopened, err := os.OpenRoot(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			replay, err := DrainDeployment(context.Background(), reopened, input.SkillDeploymentIdentity)
			if err != nil || replay != first {
				t.Fatal("restart changed durable drain", err)
			}
			if _, err := PrepareDeployment(context.Background(), reopened, input, func(context.Context, string) (io.ReadCloser, error) {
				t.Error("drained attempt downloaded bytes")
				return nil, errors.New("unexpected read")
			}, DefaultCopyPolicy()); !errors.Is(err, ErrDeploymentDrained) {
				t.Fatal("late preparation was not fenced", err)
			}
			if prepared {
				saved, err := ReadDeploymentPreparation(context.Background(), reopened, input, DefaultCopyPolicy())
				if err != nil || saved != original {
					t.Fatal("drain changed original prepared content", err)
				}
			} else if _, err := reopened.Lstat(deploymentBundleName(input.AttemptID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("drain created a prepared directory", err)
			}
			changed := input.SkillDeploymentIdentity
			changed.TaskID = changed.AccountID
			if _, err := DrainDeployment(context.Background(), reopened, changed); err == nil {
				t.Fatal("changed identity replaced a drain")
			}
			changedInput := input
			changedInput.TaskID = changed.TaskID
			if _, err := PrepareDeployment(context.Background(), reopened, changedInput, open, DefaultCopyPolicy()); err == nil {
				t.Fatal("changed identity bypassed drain")
			}
			info, err := os.Stat(filepath.Join(path, deploymentDrainName(input.AttemptID)))
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatal("drain is not private", err)
			}
		})
	}
}

func TestDeploymentDrainPreservesCorruptPreparedContent(t *testing.T) {
	store, path := privateStore(t)
	input, open := deploymentFixture(t)
	fenceDeployment(t, store, input)
	if _, err := PrepareDeployment(context.Background(), store, input, open, DefaultCopyPolicy()); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, deploymentBundleName(input.AttemptID), "work/learning/SKILL.md")
	if err := os.WriteFile(file, []byte("retain damaged evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := DrainDeployment(context.Background(), store, input.SkillDeploymentIdentity); err != nil {
		t.Fatal("corrupt content prevented safe fence", err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "retain damaged evidence" {
		t.Fatal("drain repaired or removed evidence", err)
	}
}

func TestDeploymentDrainRejectsCorruptFenceWithoutRepair(t *testing.T) {
	for _, kind := range []string{"json", "alias", "duplicate", "null", "symlink", "hardlink", "mode", "directory", "owner"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "owner" && os.Geteuid() != 0 {
				t.Skip("requires chown authority")
			}
			store, path := privateStore(t)
			input, open := deploymentFixture(t)
			fenceDeployment(t, store, input)
			if _, err := DrainDeployment(context.Background(), store, input.SkillDeploymentIdentity); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(path, deploymentDrainName(input.AttemptID))
			var err error
			switch kind {
			case "json":
				err = os.WriteFile(file, []byte(`{}`), 0o600)
			case "alias":
				err = os.WriteFile(file, []byte(`{"Version":1}`), 0o600)
			case "duplicate":
				err = os.WriteFile(file, []byte(`{"version":1,"version":1}`), 0o600)
			case "null":
				err = os.WriteFile(file, []byte(`null`), 0o600)
			case "symlink":
				err = os.Rename(file, file+".original")
				if err == nil {
					err = os.Symlink(file+".original", file)
				}
			case "hardlink":
				err = os.Link(file, file+".link")
			case "mode":
				err = os.Chmod(file, 0o644)
			case "directory":
				err = os.Remove(file)
				if err == nil {
					err = os.Mkdir(file, 0o700)
				}
			case "owner":
				err = os.Chown(file, 12345, 12345)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DrainDeployment(context.Background(), store, input.SkillDeploymentIdentity); err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatal("corruption became absence or success", err)
			}
			if _, err := PrepareDeployment(context.Background(), store, input, open, DefaultCopyPolicy()); err == nil {
				t.Fatal("damaged fence allowed preparation")
			}
			if _, err := os.Lstat(file); err != nil {
				t.Fatal("damaged drain removed", err)
			}
		})
	}
}

func TestDeploymentDrainRequiresOriginalAccountFenceAndContext(t *testing.T) {
	for _, kind := range []string{"missing", "foreign", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			store, _ := privateStore(t)
			input, _ := deploymentFixture(t)
			if kind != "missing" {
				fenced := input
				if kind == "foreign" {
					fenced.UserID = input.NodeID
				}
				fenceDeployment(t, store, fenced)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			if _, err := DrainDeployment(ctx, store, input.SkillDeploymentIdentity); err == nil {
				t.Fatal("invalid authority accepted")
			}
			if _, err := store.Lstat(deploymentDrainName(input.AttemptID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid drain wrote a fence", err)
			}
		})
	}
}

func TestDeploymentDrainCannotInventOriginalIdentityForDamagedBundle(t *testing.T) {
	for _, kind := range []string{"missing_receipt", "corrupt_receipt", "linked_bundle"} {
		t.Run(kind, func(t *testing.T) {
			store, path := privateStore(t)
			input, open := deploymentFixture(t)
			fenceDeployment(t, store, input)
			if _, err := PrepareDeployment(context.Background(), store, input, open, DefaultCopyPolicy()); err != nil {
				t.Fatal(err)
			}
			bundle := filepath.Join(path, deploymentBundleName(input.AttemptID))
			var err error
			switch kind {
			case "missing_receipt":
				err = os.Remove(filepath.Join(bundle, "deployment.json"))
			case "corrupt_receipt":
				err = os.WriteFile(filepath.Join(bundle, "deployment.json"), []byte(`{}`), 0o600)
			case "linked_bundle":
				err = os.Rename(bundle, bundle+".original")
				if err == nil {
					err = os.Symlink(bundle+".original", bundle)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DrainDeployment(context.Background(), store, input.SkillDeploymentIdentity); err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatal("unidentified original bundle was drained", err)
			}
			if _, err := store.Lstat(deploymentDrainName(input.AttemptID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unsafe drain wrote a fence", err)
			}
		})
	}
}
