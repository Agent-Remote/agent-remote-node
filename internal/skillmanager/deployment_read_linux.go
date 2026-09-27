package skillmanager

import (
	"context"
	"errors"
	"os"
	"syscall"
)

// ReadDeploymentPreparation verifies immutable local evidence and all complete retained bytes.
// Only an absent bundle returns os.ErrNotExist; damaged existing state must never be repaired by retry.
func ReadDeploymentPreparation(ctx context.Context, store *os.Root, input SkillDeployment, policy CopyPolicy) (DeploymentPreparation, error) {
	if err := input.Validate(input.SkillDeploymentIdentity); err != nil {
		return DeploymentPreparation{}, err
	}
	if _, err := validateCopyManifest(input.Manifest, policy, nil); err != nil {
		return DeploymentPreparation{}, err
	}
	if err := requireDeploymentFence(store, input); err != nil {
		return DeploymentPreparation{}, err
	}
	if err := ctx.Err(); err != nil {
		return DeploymentPreparation{}, err
	}
	name := deploymentBundleName(input.AttemptID)
	info, err := store.Lstat(name)
	if err != nil {
		return DeploymentPreparation{}, err
	}
	if !info.IsDir() {
		return DeploymentPreparation{}, errors.New("deployment bundle is not a directory")
	}
	bundle, err := store.OpenRoot(name)
	if err != nil {
		return DeploymentPreparation{}, errors.New("deployment bundle cannot be opened")
	}
	defer bundle.Close()
	receipt, err := readDeploymentBundle(ctx, bundle, input, policy)
	if err != nil {
		if ctx.Err() != nil {
			return DeploymentPreparation{}, ctx.Err()
		}
		return DeploymentPreparation{}, errors.New("retained deployment is incomplete or invalid")
	}
	// A previous publication may have lost its parent fsync acknowledgement.
	if err := syncDirectory(store, "."); err != nil {
		return DeploymentPreparation{}, err
	}
	return receipt, nil
}

func readDeploymentBundle(ctx context.Context, bundle *os.Root, input SkillDeployment, policy CopyPolicy) (DeploymentPreparation, error) {
	var receipt DeploymentPreparation
	if err := readPrivateJSON(bundle, "deployment.json", 1<<20, &receipt); err != nil {
		return receipt, err
	}
	if err := receipt.Validate(input); err != nil {
		return receipt, err
	}
	var baseline PermissionBaseline
	if err := readPrivateJSON(bundle, "baseline.json", maxBaselineBytes, &baseline); err != nil {
		return receipt, err
	}
	digest, err := Digest(baseline.Source)
	if err != nil || digest != input.TreeDigest {
		return receipt, errors.New("deployment baseline differs from original tree")
	}
	info, err := bundle.Lstat("work")
	if err != nil || !info.IsDir() {
		return receipt, errors.New("deployment work is not a directory")
	}
	work, err := bundle.OpenRoot("work")
	if err != nil {
		return receipt, err
	}
	defer work.Close()
	directory, err := privateBundleFile(work)
	if err != nil {
		return receipt, err
	}
	_ = directory.Close()
	for _, entry := range baseline.Materialized.Entries {
		info, err := work.Lstat(entry.Path)
		if err != nil {
			return receipt, err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Gid != uint32(os.Getegid()) || stat.Mode&0o7777 != entry.Mode || info.Mode().IsRegular() && stat.Nlink != 1 {
			return receipt, errors.New("deployment content lost its private ownership")
		}
	}
	manifest, err := CaptureWorkTree(ctx, work, baseline, CaptureOptions{DirectoryBytes: policy.DirectoryBytes, Entries: policy.Entries})
	if err != nil {
		return receipt, err
	}
	digest, err = Digest(manifest)
	if err != nil || digest != input.TreeDigest {
		return receipt, errors.New("deployment bytes differ from original tree")
	}
	return receipt, nil
}
