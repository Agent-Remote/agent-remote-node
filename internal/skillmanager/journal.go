package skillmanager

import "errors"

// SnapshotBinding retains the authorization identity after the transient runtime spec is removed.
type SnapshotBinding struct {
	UserID            string            `json:"user_id"`
	AccountID         string            `json:"account_id"`
	NodeID            string            `json:"node_id"`
	SessionID         string            `json:"session_id"`
	SnapshotID        string            `json:"snapshot_id"`
	DirectoryEpoch    int64             `json:"directory_epoch"`
	LibraryGeneration int64             `json:"library_generation"`
	InitialTreeDigest string            `json:"initial_tree_digest"`
	TaskID            string            `json:"task_id,omitempty"`
	PreparationDigest string            `json:"preparation_digest,omitempty"`
	SystemReleases    SystemReleasePins `json:"system_releases,omitzero"`
}

// PreparedSnapshot is sealed by the helper before any runtime may receive the prepared work tree.
type PreparedSnapshot struct {
	Version int             `json:"version"`
	Binding SnapshotBinding `json:"binding"`
	Capture CaptureOptions  `json:"capture"`
}

// FinalizationRecord separates local durability, Server persistence and complete publication.
type FinalizationRecord struct {
	Version        int             `json:"version"`
	ObjectsVersion int             `json:"objects_version,omitempty"`
	Binding        SnapshotBinding `json:"binding"`
	TreeDigest     string          `json:"tree_digest"`
	Unclean        bool            `json:"unclean"`
	State          string          `json:"state"`
}

// Validate verifies the original owner/epoch identity without filesystem or runtime access.
func (binding SnapshotBinding) Validate() error {
	for _, value := range []string{binding.UserID, binding.AccountID, binding.NodeID, binding.SessionID, binding.SnapshotID} {
		if !validSkillUUID(value) {
			return errors.New("invalid skill snapshot identity")
		}
	}
	if binding.DirectoryEpoch <= 0 || binding.LibraryGeneration < 0 || !contentDigestPattern.MatchString(binding.InitialTreeDigest) {
		return errors.New("invalid skill snapshot generation or tree identity")
	}
	if (binding.TaskID != "" || binding.PreparationDigest != "") && (!validSkillUUID(binding.TaskID) || !contentDigestPattern.MatchString(binding.PreparationDigest)) {
		return errors.New("invalid skill preparation identity")
	}
	if binding.SystemReleases != (SystemReleasePins{}) {
		return binding.SystemReleases.Validate()
	}
	return nil
}

// Validate checks the immutable finalization identity and supported local persistence phase.
func (record FinalizationRecord) Validate() error {
	if err := record.Binding.Validate(); err != nil {
		return err
	}
	if record.Version != 1 || record.ObjectsVersion < 0 || record.ObjectsVersion > 1 ||
		!contentDigestPattern.MatchString(record.TreeDigest) || !validFinalizationState(record.State, record.Unclean) {
		return errors.New("invalid skill finalization record")
	}
	return nil
}

// CanDeleteSession reports whether Server-retained finalization permits deleting the session view.
// It never authorizes removal of local content; local retention requires a separate reference check.
func (r FinalizationRecord) CanDeleteSession() bool {
	return r.Version == 1 && validFinalizationState(r.State, r.Unclean) && (r.State == "published" || r.State == "conflicted" || r.State == "detached")
}

func validFinalizationState(state string, unclean bool) bool {
	if state == "local_durable" || state == "upload_pending" || state == "detached" {
		return true
	}
	if unclean {
		return state == "persisted_unclean"
	}
	return state == "persisted" || state == "published" || state == "conflicted"
}

func allowedFinalizationTransition(current, next string, unclean bool) bool {
	if !validFinalizationState(next, unclean) {
		return false
	}
	switch current {
	case "local_durable":
		return next == "upload_pending"
	case "upload_pending":
		return next == "persisted" || next == "persisted_unclean"
	case "persisted":
		return next == "published" || next == "conflicted" || next == "detached"
	case "persisted_unclean":
		return next == "detached"
	}
	return false
}
