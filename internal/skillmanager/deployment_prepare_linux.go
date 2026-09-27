package skillmanager

import (
	"context"
	"errors"
	"os"
)

// PrepareDeployment atomically retains an independent, Helper-owned complete directory.
// Callers must authorize the original input and serialize preparation with disk admission.
func PrepareDeployment(ctx context.Context, store *os.Root, input SkillDeployment, open ObjectOpener, policy CopyPolicy) (DeploymentPreparation, error) {
	digest, err := input.InputDigest()
	if err != nil {
		return DeploymentPreparation{}, err
	}
	if _, err := ReadDeploymentDrain(store, input.SkillDeploymentIdentity); err == nil {
		return DeploymentPreparation{}, ErrDeploymentDrained
	} else if !errors.Is(err, os.ErrNotExist) {
		return DeploymentPreparation{}, err
	}
	baseline, err := validateCopyManifest(input.Manifest, policy, nil)
	if err != nil {
		return DeploymentPreparation{}, err
	}
	previous, err := ReadDeploymentPreparation(ctx, store, input, policy)
	if err == nil {
		return previous, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return DeploymentPreparation{}, err
	}
	receipt := DeploymentPreparation{Version: 1, Binding: input.SkillDeploymentIdentity, InputDigest: digest,
		DirectoryEpoch: input.DirectoryEpoch, Generation: input.Plan.Generation, HelperReceiptID: newCaptureID()}
	options := MaterializeOptions{UID: os.Geteuid(), GID: os.Getegid(), Policy: policy}
	_, err = materializeValidated(ctx, store, deploymentBundleName(input.AttemptID), baseline, open, options, func(bundle *os.Root) error {
		directory, err := privateBundleFile(bundle)
		if err != nil {
			return err
		}
		defer directory.Close()
		return writePrivateJSON(bundle, directory, "deployment.json", receipt, true)
	})
	if err != nil {
		return DeploymentPreparation{}, err
	}
	return receipt, nil
}

func deploymentBundleName(attemptID string) string { return "deployment-" + attemptID }

func requireDeploymentFence(store *os.Root, input SkillDeployment) error {
	fence, err := ReadAccountFence(store, input.NodeID, input.UserID, input.AccountID)
	if err != nil || fence.DirectoryEpoch > input.DirectoryEpoch {
		// Missing authority must not be mistaken for an absent prepared bundle.
		return errors.New("deployment requires its original account fence")
	}
	return nil
}
