package skillmanager

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func validatePreparation(bundle string, source Manifest, options MaterializeOptions) (PermissionBaseline, error) {
	if err := ValidatePath(bundle); err != nil {
		return PermissionBaseline{}, err
	}
	if strings.Contains(bundle, "/") || strings.HasPrefix(bundle, ".") {
		return PermissionBaseline{}, errors.New("skill bundle must be one non-hidden identifier")
	}
	if options.UID <= 0 || options.GID <= 0 || options.UID > math.MaxInt32 || options.GID > math.MaxInt32 {
		return PermissionBaseline{}, errors.New("skill runtime identity must be non-root")
	}
	return validateCopyManifest(source, options.Policy, options.RuntimeDependencies)
}

func validateCopyManifest(source Manifest, policy CopyPolicy, dependencies map[string]string) (PermissionBaseline, error) {
	if policy.DirectoryBytes <= 0 || policy.Entries <= 0 || policy.Entries > 100_000 || policy.ReservePercent > 100 {
		return PermissionBaseline{}, errors.New("invalid skill copy policy")
	}
	baseline, err := WritableBaseline(source)
	if err != nil {
		return PermissionBaseline{}, err
	}
	if len(source.Entries) > policy.Entries {
		return PermissionBaseline{}, errors.New("quota_exceeded: skill directory entry limit")
	}
	var size int64
	for _, entry := range source.Entries {
		if entry.Size > policy.DirectoryBytes-size {
			return PermissionBaseline{}, errors.New("quota_exceeded: skill directory byte limit")
		}
		size += entry.Size
		if entry.Kind == "runtime_link" && dependencies[entry.Dependency] != entry.Target {
			return PermissionBaseline{}, errors.New("runtime_dependency_missing: unverified skill runtime link")
		}
		if entry.Path == "ego-browser" || strings.HasPrefix(entry.Path, "ego-browser/") ||
			entry.Path == "agent-remote-device" || strings.HasPrefix(entry.Path, "agent-remote-device/") {
			return PermissionBaseline{}, errors.New("skill manifest includes a reserved system entry")
		}
	}
	return baseline, nil
}

func verifyPrivateStore(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("skill store must be private and owned by the helper identity")
	}
	return nil
}

func checkDiskSpace(file *os.File, source Manifest, policy CopyPolicy) error {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		return err
	}
	if stat.Bsize <= 0 || stat.Blocks > math.MaxUint64/uint64(stat.Bsize) || stat.Bavail > math.MaxUint64/uint64(stat.Bsize) {
		return errors.New("invalid skill filesystem capacity")
	}
	block := uint64(stat.Bsize)
	capacity, available := stat.Blocks*block, stat.Bavail*block
	reserve := max(policy.MinimumFreeBytes, capacity/100*policy.ReservePercent)
	// Count actual encoded metadata, including JSON escaping, rather than assuming short paths.
	required := uint64(3)*block + 1024
	for _, entry := range source.Entries {
		encoded, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		extra := uint64(entry.Size) + block + uint64(len(encoded)+8)*2
		if required > math.MaxUint64-extra {
			return errors.New("skill expanded disk estimate overflow")
		}
		required += extra
	}
	if available < reserve || required > available-reserve {
		return errors.New("insufficient_storage: skill copy would consume the filesystem reserve")
	}
	if stat.Files > 0 && stat.Ffree < uint64(len(source.Entries))+3 {
		return errors.New("insufficient_storage: insufficient skill filesystem inodes")
	}
	return nil
}
