package skillmanager

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// CaptureAccountSource retains a complete, already-quiescent account directory without mutating it.
// The caller must hold Helper serialization and prove all writers exited after closing the fence.
// A nil source represents a confirmed missing discovery root, never an inspection error.
func CaptureAccountSource(ctx context.Context, store, source *os.Root, binding AccountTakeoverBinding, policy CopyPolicy) (AccountCapture, error) {
	if err := binding.Validate(); err != nil {
		return AccountCapture{}, err
	}
	if err := requireCaptureFence(store, binding); err != nil {
		return AccountCapture{}, err
	}
	bundle, previous, _, err := OpenAccountCapture(store, binding)
	if err == nil {
		_ = bundle.Close()
		return previous, ctx.Err()
	}
	if !errors.Is(err, os.ErrNotExist) {
		return AccountCapture{}, err
	}
	if policy.DirectoryBytes <= 0 || policy.Entries <= 0 || policy.Entries > 100_000 || policy.ReservePercent > 100 {
		return AccountCapture{}, errors.New("invalid account capture policy")
	}
	manifest, err := captureAccountTree(ctx, source, policy)
	if err != nil {
		return AccountCapture{}, err
	}
	directory, err := privateBundleFile(store)
	if err != nil {
		return AccountCapture{}, err
	}
	defer directory.Close()
	if err := checkDiskSpace(directory, manifest, policy); err != nil {
		return AccountCapture{}, err
	}
	staging := ".takeover-" + rand.Text()
	if err := store.Mkdir(staging, 0o700); err != nil {
		return AccountCapture{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = store.RemoveAll(staging)
		}
	}()
	stage, err := store.OpenRoot(staging)
	if err != nil {
		return AccountCapture{}, err
	}
	defer stage.Close()
	if err := copyRetainedObjects(ctx, stage, source, manifest); err != nil {
		return AccountCapture{}, err
	}
	verified, err := captureAccountTree(ctx, source, policy)
	if err != nil {
		return AccountCapture{}, err
	}
	digest, err := Digest(manifest)
	if err != nil {
		return AccountCapture{}, err
	}
	checked, err := Digest(verified)
	if err != nil || checked != digest {
		return AccountCapture{}, errors.New("account source changed during capture")
	}
	receipt := AccountCapture{Version: 1, Binding: binding, HelperReceiptID: newCaptureID(), TreeDigest: digest, SourceExists: source != nil}
	if err := sealAccountCapture(stage, receipt, manifest); err != nil {
		return AccountCapture{}, err
	}
	if err := ctx.Err(); err != nil {
		return AccountCapture{}, err
	}
	if err := unix.Renameat2(int(directory.Fd()), staging, int(directory.Fd()), accountCaptureName(binding.AccountID), unix.RENAME_NOREPLACE); err != nil {
		return AccountCapture{}, err
	}
	published = true
	if err := directory.Sync(); err != nil {
		return AccountCapture{}, err
	}
	return receipt, nil
}

func captureAccountTree(ctx context.Context, source *os.Root, policy CopyPolicy) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	empty := Manifest{Version: 1}
	if source == nil {
		return empty, nil
	}
	return CaptureWorkTree(ctx, source, PermissionBaseline{Version: 1, Source: empty, Materialized: empty}, CaptureOptions{
		DirectoryBytes: policy.DirectoryBytes, Entries: policy.Entries,
		SystemPaths: []string{"ego-browser", "agent-remote-device"},
	})
}

func sealAccountCapture(stage *os.Root, receipt AccountCapture, manifest Manifest) error {
	file, err := stage.OpenFile("manifest.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = errors.Join(writeManifest(file, manifest), file.Sync(), file.Close())
	if err != nil {
		return err
	}
	directory, err := privateBundleFile(stage)
	if err != nil {
		return err
	}
	defer directory.Close()
	return writePrivateJSON(stage, directory, "record.json", receipt, true)
}

func requireCaptureFence(store *os.Root, binding AccountTakeoverBinding) error {
	fence, err := ReadAccountFence(store, binding.NodeID, binding.UserID, binding.AccountID)
	if err != nil {
		return err
	}
	if fence.DirectoryEpoch != binding.DirectoryEpoch {
		return errors.New("account capture fence epoch differs from reservation")
	}
	return nil
}

func accountCaptureName(accountID string) string {
	return "takeover-" + accountID
}

func newCaptureID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	value[6], value[8] = value[6]&0x0f|0x40, value[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:])
}
