package skillmanager

// RecoveryFileVerifier hashes bounded file bytes before receiving the final hash/classification.
// Its provisional entry cannot be used as a v1 manifest entry until Complete succeeds.
type RecoveryFileVerifier struct {
	verifier *ContentVerifier
}

// NewRecoveryFileVerifier accepts only a file start with absent content claims.
func NewRecoveryFileVerifier(start Entry) (*RecoveryFileVerifier, error) {
	if err := ValidateRecoveryStart(start); err != nil {
		return nil, err
	}
	start.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	start.ContentKind = "binary"
	verifier, err := NewContentVerifier(start)
	if err != nil {
		return nil, err
	}
	return &RecoveryFileVerifier{verifier: verifier}, nil
}

// Write hashes each bounded block without retaining content and rejects declared-size overflow.
func (v *RecoveryFileVerifier) Write(data []byte) (int, error) { return v.verifier.Write(data) }

// Complete validates the trailing claims against every byte actually consumed.
func (v *RecoveryFileVerifier) Complete(digest, kind string) (Entry, error) {
	v.verifier.entry.SHA256, v.verifier.entry.ContentKind = digest, kind
	if err := validateEntry(v.verifier.entry); err != nil {
		return Entry{}, err
	}
	if err := v.verifier.Finish(); err != nil {
		return Entry{}, err
	}
	return v.verifier.entry, nil
}
