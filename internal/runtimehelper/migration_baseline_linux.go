package runtimehelper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// Baselines are private historical evidence; inventory observations additionally detect
// substitutions or concurrent changes within one live, caller-excluded inspection.
type migrationCompletionObservation struct {
	inventory [2]migrationInventory
	baseline  [sha256.Size]byte
	parents   [sha256.Size]byte
}

func (e Engine) captureMigrationBaseline(ctx context.Context, store *os.Root, original skillmanager.AccountMigrationReceipt, account string) error {
	first, err := scanMigrationInventory(ctx, account)
	if err != nil {
		return errMigrationWritersUnknown
	}
	parents, err := e.migrationParentPermissions(ctx, account)
	if err != nil {
		return errMigrationWritersUnknown
	}
	again, err := scanMigrationInventory(ctx, account)
	if err != nil || again != first {
		return errMigrationWritersUnknown
	}
	after, err := e.migrationParentPermissions(ctx, account)
	if err != nil || !reflect.DeepEqual(parents, after) || ctx.Err() != nil || currentBootID() != original.Copy.BootID {
		return errMigrationWritersUnknown
	}
	baseline := skillmanager.AccountMigrationBaseline{Version: 1, Migration: original,
		Account:       skillmanager.MigrationObjectIdentity{DeviceMajor: first.root.deviceMajor, DeviceMinor: first.root.deviceMinor, Inode: first.root.inode},
		ContentDigest: hex.EncodeToString(first.content[:]), PermissionsDigest: hex.EncodeToString(first.permissions[:]), Parents: parents}
	if err := skillmanager.BeginAccountMigrationBaseline(store, baseline); err != nil {
		return errMigrationWritersUnknown
	}
	return nil
}

func (e Engine) inspectMigrationCompletion(ctx context.Context, store *os.Root, original skillmanager.AccountMigrationReceipt, account, backup, outcome string) (migrationCompletionObservation, error) {
	var observation migrationCompletionObservation
	pair, err := compareMigrationInventory(ctx, account, backup)
	if err != nil {
		return observation, err
	}
	observation.inventory = pair
	if original.Version == 1 {
		return observation, nil
	}
	baseline, err := skillmanager.ReadAccountMigrationBaseline(store, original)
	if err != nil || baseline.ContentDigest != hex.EncodeToString(pair[1].content[:]) || baseline.PermissionsDigest != hex.EncodeToString(pair[1].permissions[:]) ||
		baseline.Account != (skillmanager.MigrationObjectIdentity{DeviceMajor: pair[0].root.deviceMajor, DeviceMinor: pair[0].root.deviceMinor, Inode: pair[0].root.inode}) {
		return observation, errMigrationWritersUnknown
	}
	parents, err := e.migrationParentPermissions(ctx, account)
	if err != nil || len(parents) != len(baseline.Parents) {
		return observation, errMigrationWritersUnknown
	}
	for index, parent := range parents {
		if parent.Path != baseline.Parents[index].Path || parent.Identity != baseline.Parents[index].Identity {
			return observation, errMigrationWritersUnknown
		}
	}
	if outcome == "failed" && (pair[0].permissions != pair[1].permissions || !reflect.DeepEqual(parents, baseline.Parents)) {
		return observation, errMigrationWritersUnknown
	}
	data, _ := json.Marshal(baseline)
	observation.baseline = sha256.Sum256(data)
	data, _ = json.Marshal(parents)
	observation.parents = sha256.Sum256(data)
	return observation, ctx.Err()
}

func attestMigrationCompletion(store *os.Root, original skillmanager.AccountMigrationReceipt, outcome string) error {
	if original.Version == 1 {
		return nil
	}
	// A terminal original already has its immutable attestation. Revalidation never rewrites it.
	if original.State != "started" {
		return skillmanager.CheckAccountMigrationWriter(store, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID, original.Copy.TaskID)
	}
	return skillmanager.AttestAccountMigration(store, original, outcome)
}
