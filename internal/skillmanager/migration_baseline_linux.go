package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// MigrationObjectIdentity pins an original directory without relying on its mutable path.
type MigrationObjectIdentity struct {
	DeviceMajor uint32 `json:"device_major"`
	DeviceMinor uint32 `json:"device_minor"`
	Inode       uint64 `json:"inode"`
}

// MigrationParentPermissions retains the traversal ACLs changed by backend migration.
// AttributeDigest also covers attributes that the migration is not allowed to change.
type MigrationParentPermissions struct {
	Path            string                  `json:"path"`
	Identity        MigrationObjectIdentity `json:"identity"`
	Mode            uint32                  `json:"mode"`
	UID             uint32                  `json:"uid"`
	GID             uint32                  `json:"gid"`
	AccessACL       []byte                  `json:"access_acl"`
	DefaultACL      []byte                  `json:"default_acl"`
	AttributeDigest string                  `json:"attribute_digest"`
}

// AccountMigrationBaseline is immutable private evidence captured before the original copy.
// It contains no account file bytes and is never returned through the Helper protocol.
type AccountMigrationBaseline struct {
	Version           int                          `json:"version"`
	Migration         AccountMigrationReceipt      `json:"migration"`
	Account           MigrationObjectIdentity      `json:"account"`
	ContentDigest     string                       `json:"content_digest"`
	PermissionsDigest string                       `json:"permissions_digest"`
	Parents           []MigrationParentPermissions `json:"parents"`
}

func migrationBaselineName(task string) string { return "migration-baseline-" + accountCopyName(task) }

func validateMigrationBaseline(b AccountMigrationBaseline) error {
	if b.Version != 1 || validateAccountMigration(b.Migration) != nil || b.Migration.Version != 2 || b.Migration.State != "started" || b.Account.Inode == 0 ||
		!contentDigestPattern.MatchString(b.ContentDigest) || !contentDigestPattern.MatchString(b.PermissionsDigest) || len(b.Parents) == 0 || len(b.Parents) > 128 {
		return errors.New("invalid migration baseline")
	}
	bytes := 0
	for index, parent := range b.Parents {
		bytes += len(parent.Path) + len(parent.AccessACL) + len(parent.DefaultACL)
		if !filepath.IsAbs(parent.Path) || filepath.Clean(parent.Path) != parent.Path || strings.ContainsRune(parent.Path, '\x00') || len(parent.Path) > 4096 ||
			parent.Identity.Inode == 0 || parent.Mode&unix.S_IFMT != unix.S_IFDIR || parent.Mode & ^uint32(unix.S_IFMT|07777) != 0 ||
			!contentDigestPattern.MatchString(parent.AttributeDigest) || len(parent.AccessACL) > 64<<10 || len(parent.DefaultACL) > 64<<10 || bytes > 256<<10 {
			return errors.New("invalid migration parent permissions")
		}
		if index > 0 && filepath.Dir(parent.Path) != b.Parents[index-1].Path {
			return errors.New("migration parents are not one ordered chain")
		}
	}
	return nil
}

// BeginAccountMigrationBaseline seals the original tree and parents before any copy or ownership.
func BeginAccountMigrationBaseline(root *os.Root, baseline AccountMigrationBaseline) error {
	if err := validateMigrationBaseline(baseline); err != nil {
		return err
	}
	stored, err := readAccountMigration(root, baseline.Migration.Copy.TaskID)
	if err != nil || stored != baseline.Migration {
		return errors.New("baseline lacks original pending migration")
	}
	if _, err := ReadAccountCopy(root, stored.Copy); !errors.Is(err, os.ErrNotExist) {
		return errors.New("baseline must precede the original copy")
	}
	private, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer private.Close()
	return writePrivateJSON(root, private, migrationBaselineName(stored.Copy.TaskID), baseline, true)
}

// ReadAccountMigrationBaseline verifies original version, boot, input and private record integrity.
func ReadAccountMigrationBaseline(root *os.Root, original AccountMigrationReceipt) (AccountMigrationBaseline, error) {
	var baseline AccountMigrationBaseline
	if err := validateAccountMigration(original); err != nil {
		return baseline, err
	}
	if err := readPrivateJSON(root, migrationBaselineName(original.Copy.TaskID), 1<<20, &baseline); err != nil {
		return baseline, err
	}
	if err := validateMigrationBaseline(baseline); err != nil {
		return baseline, err
	}
	original.State = "started"
	if original != baseline.Migration {
		return baseline, errors.New("migration baseline authority differs")
	}
	return baseline, nil
}

func migrationBaselineDigest(baseline AccountMigrationBaseline) string {
	data, _ := json.Marshal(baseline)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func readMigrationEvidenceOwner(root *os.Root, name string) (AccountMigrationReceipt, error) {
	var original AccountMigrationReceipt
	if strings.HasPrefix(name, "migration-baseline-") {
		var baseline AccountMigrationBaseline
		if err := readPrivateJSON(root, name, 1<<20, &baseline); err != nil {
			return original, err
		}
		if validateMigrationBaseline(baseline) != nil || name != migrationBaselineName(baseline.Migration.Copy.TaskID) {
			return original, errors.New("invalid local migration baseline")
		}
		original = baseline.Migration
	} else {
		var attestation migrationAttestation
		if err := readPrivateJSON(root, name, 1<<20, &attestation); err != nil {
			return original, err
		}
		original = attestation.Migration
		if original.Version != 2 || name != migrationAttestationName(original.Copy.TaskID) || requireMigrationAttestation(root, original, attestation.Outcome) != nil {
			return original, errors.New("invalid local migration attestation")
		}
	}
	saved, err := readAccountMigration(root, original.Copy.TaskID)
	saved.State = "started"
	if err != nil || saved != original {
		return original, errors.New("orphaned migration evidence")
	}
	return original, nil
}
