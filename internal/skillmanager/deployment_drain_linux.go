package skillmanager

import (
	"context"
	"errors"
	"os"
)

// ReadDeploymentDrain reads an exact permanent preparation fence without examining retained content.
// Only an absent drain record returns os.ErrNotExist; missing account authority is never absence.
func ReadDeploymentDrain(store *os.Root, binding SkillDeploymentIdentity) (DeploymentDrain, error) {
	if err := binding.Validate(); err != nil {
		return DeploymentDrain{}, err
	}
	if _, err := ReadAccountFence(store, binding.NodeID, binding.UserID, binding.AccountID); err != nil {
		return DeploymentDrain{}, errors.New("deployment drain requires its original account fence")
	}
	var receipt DeploymentDrain
	if err := readPrivateJSON(store, deploymentDrainName(binding.AttemptID), 4096, &receipt); err != nil {
		return receipt, err
	}
	return receipt, receipt.Validate(binding)
}

// DrainDeployment permanently fences an original attempt while preserving all retained evidence.
// Callers must serialize this operation with preparation, including its complete copy/publication.
func DrainDeployment(ctx context.Context, store *os.Root, binding SkillDeploymentIdentity) (DeploymentDrain, error) {
	if err := ctx.Err(); err != nil {
		return DeploymentDrain{}, err
	}
	receipt, err := ReadDeploymentDrain(store, binding)
	if err == nil {
		// Retry completes any parent fsync whose acknowledgement was lost after publication.
		return receipt, syncDirectory(store, ".")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return DeploymentDrain{}, err
	}
	if err := requireOriginalDeploymentBundle(store, binding); err != nil {
		return DeploymentDrain{}, err
	}
	directory, err := privateBundleFile(store)
	if err != nil {
		return DeploymentDrain{}, err
	}
	defer directory.Close()
	if err := ctx.Err(); err != nil {
		return DeploymentDrain{}, err
	}
	receipt = DeploymentDrain{Version: 1, Binding: binding, HelperReceiptID: newCaptureID()}
	if err := writePrivateJSON(store, directory, deploymentDrainName(binding.AttemptID), receipt, true); err != nil {
		if errors.Is(err, os.ErrExist) {
			saved, readErr := ReadDeploymentDrain(store, binding)
			if readErr != nil {
				return DeploymentDrain{}, readErr
			}
			return saved, directory.Sync()
		}
		return DeploymentDrain{}, err
	}
	return receipt, ctx.Err()
}

func deploymentDrainName(attemptID string) string { return "deployment-drain-" + attemptID + ".json" }

func requireOriginalDeploymentBundle(store *os.Root, binding SkillDeploymentIdentity) error {
	name := deploymentBundleName(binding.AttemptID)
	info, err := store.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("deployment drain found an unsafe prepared bundle")
	}
	bundle, err := store.OpenRoot(name)
	if err != nil {
		return err
	}
	defer bundle.Close()
	var receipt DeploymentPreparation
	if err := readPrivateJSON(bundle, "deployment.json", 4096, &receipt); err != nil {
		// An existing bundle with missing identity cannot be treated as an unprepared attempt.
		return errors.New("deployment drain cannot identify its prepared bundle")
	}
	if receipt.ValidateBinding() != nil || receipt.Binding != binding {
		return errors.New("deployment drain differs from its prepared bundle")
	}
	return nil
}
