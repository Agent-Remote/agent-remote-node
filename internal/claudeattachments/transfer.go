package claudeattachments

// This file implements the exact-session attachment wire receiver. It is
// intentionally independent from the legacy account receiver: authorization
// is performed by the exact Server lease before this channel is opened.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	AttachmentProtocolVersion = 1
	MaxAttachmentHeaderBytes  = 64 * 1024
	MaxAttachmentBatchBytes   = 256 * 1024 * 1024
	MaxAttachmentEntries      = 32
	MaxAttachmentNameBytes    = 255
)

// TransferIdentity is the immutable identity bound to one gateway lease.
type TransferIdentity struct {
	NodeID           string
	SessionID        string
	ExpectedDeviceID string
	SSHKeyID         string
	LeaseID          string
}

func (i TransferIdentity) valid() bool {
	return canonicalUUID(i.NodeID) && canonicalUUID(i.SessionID) && canonicalUUID(i.ExpectedDeviceID) && canonicalUUID(i.SSHKeyID) && canonicalUUID(i.LeaseID)
}

// OpenFrame starts a strict exact-session transfer stream.
type OpenFrame struct {
	Version          int    `json:"version"`
	Operation        string `json:"operation"`
	SessionID        string `json:"session_id"`
	ExpectedDeviceID string `json:"expected_device_id"`
	LeaseID          string `json:"lease_id"`
}

// UploadFrame declares one file or image followed immediately by size bytes.
type UploadFrame struct {
	Version          int    `json:"version"`
	Operation        string `json:"operation"`
	SessionID        string `json:"session_id"`
	ExpectedDeviceID string `json:"expected_device_id"`
	LeaseID          string `json:"lease_id"`
	BatchID          string `json:"batch_id"`
	Kind             string `json:"kind"`
	Name             string `json:"name"`
	Size             int64  `json:"size"`
	SHA256           string `json:"sha256"`
}

// CleanupFrame requests deletion scoped to exactly one lease.
type CleanupFrame struct {
	Version          int    `json:"version"`
	Operation        string `json:"operation"`
	SessionID        string `json:"session_id"`
	ExpectedDeviceID string `json:"expected_device_id"`
	LeaseID          string `json:"lease_id"`
}

// CleanupStatus is the durable result of a lease cleanup attempt.
type CleanupStatus string

const (
	CleanupCleaned  CleanupStatus = "cleaned"
	CleanupPending  CleanupStatus = "pending"
	CleanupUnknown  CleanupStatus = "unknown"
	CleanupRejected CleanupStatus = "rejected"
)

// CleanupReceipt is persisted before it is returned to the client.
type CleanupReceipt struct {
	Version          int           `json:"version"`
	Operation        string        `json:"operation"`
	NodeID           string        `json:"node_id"`
	SessionID        string        `json:"session_id"`
	ExpectedDeviceID string        `json:"expected_device_id"`
	SSHKeyID         string        `json:"ssh_key_id"`
	LeaseID          string        `json:"lease_id"`
	Status           CleanupStatus `json:"status"`
	RemovedBatches   int           `json:"removed_batches"`
	ReceiptID        string        `json:"receipt_id"`
}

// LeaseOffer is the first response on an exact transfer connection. It
// exposes only the Node-issued lease and bounded limits; account paths remain
// local to the gateway and are projected per upload acknowledgement.
type LeaseOffer struct {
	Version          int    `json:"version"`
	Operation        string `json:"operation"`
	NodeID           string `json:"node_id"`
	SessionID        string `json:"session_id"`
	ExpectedDeviceID string `json:"expected_device_id"`
	SSHKeyID         string `json:"ssh_key_id"`
	LeaseID          string `json:"lease_id"`
	ExpiresAt        string `json:"expires_at"`
	MaxBytes         int64  `json:"max_bytes"`
	MaxEntries       int    `json:"max_entries"`
}

// Validate checks a frame against the admitted lease identity.
func (f OpenFrame) Validate(identity TransferIdentity) error {
	if f.Version != AttachmentProtocolVersion || f.Operation != "open" || !identity.valid() || f.SessionID != identity.SessionID || f.ExpectedDeviceID != identity.ExpectedDeviceID || f.LeaseID != identity.LeaseID {
		return errors.New("invalid exact attachment open frame")
	}
	return nil
}

// Validate checks a frame against the admitted lease and upload bounds.
func (f UploadFrame) Validate(identity TransferIdentity, maxBytes int64, maxEntries int) error {
	if f.Version != AttachmentProtocolVersion || f.Operation != "upload" || !identity.valid() || f.SessionID != identity.SessionID || f.ExpectedDeviceID != identity.ExpectedDeviceID || f.LeaseID != identity.LeaseID || !canonicalUUID(f.BatchID) {
		return errors.New("invalid exact attachment upload identity")
	}
	if f.Kind != "file" && f.Kind != "image" {
		return errors.New("invalid exact attachment upload kind")
	}
	if !validLeafName(f.Name) || f.Size <= 0 || f.Size > MaxAttachmentBatchBytes || (maxBytes > 0 && f.Size > maxBytes) || maxEntries <= 0 || maxEntries > MaxAttachmentEntries {
		return errors.New("invalid exact attachment upload bounds")
	}
	if len(f.SHA256) != 64 || strings.ToLower(f.SHA256) != f.SHA256 {
		return errors.New("invalid exact attachment checksum")
	}
	if _, err := hex.DecodeString(f.SHA256); err != nil {
		return errors.New("invalid exact attachment checksum")
	}
	return nil
}

// Validate checks a cleanup frame against the admitted lease.
func (f CleanupFrame) Validate(identity TransferIdentity) error {
	if f.Version != AttachmentProtocolVersion || f.Operation != "cleanup" || !identity.valid() || f.SessionID != identity.SessionID || f.ExpectedDeviceID != identity.ExpectedDeviceID || f.LeaseID != identity.LeaseID {
		return errors.New("invalid exact attachment cleanup frame")
	}
	return nil
}

// Validate verifies the receipt identity and public bounds.
func (r CleanupReceipt) Validate(identity TransferIdentity) error {
	if r.Version != AttachmentProtocolVersion || r.Operation != "cleanup_receipt" || r.NodeID != identity.NodeID || r.SessionID != identity.SessionID || r.ExpectedDeviceID != identity.ExpectedDeviceID || r.SSHKeyID != identity.SSHKeyID || r.LeaseID != identity.LeaseID || !canonicalUUID(r.ReceiptID) || r.RemovedBatches < 0 || r.RemovedBatches > MaxAttachmentEntries {
		return errors.New("invalid exact attachment cleanup receipt")
	}
	switch r.Status {
	case CleanupCleaned, CleanupPending, CleanupUnknown, CleanupRejected:
		return nil
	default:
		return errors.New("invalid exact attachment cleanup status")
	}
}

// Gateway receives one already-authorized SSH stream and writes only derived
// session attachment paths. ResolveAccount must use trusted local runtime state.
type Gateway struct {
	Identity       TransferIdentity
	AccountPath    string
	LeaseExpiresAt time.Time
	MaxBytes       int64
	MaxEntries     int
	ReceiptRoot    string
	Now            func() time.Time
	// CleanupHook publishes the same receipt to the control plane. A failed
	// hook downgrades a local cleaned result to unknown.
	CleanupHook func(CleanupReceipt) error
	mu          sync.Mutex
}

// Serve reads strict newline-delimited frames and binary upload bodies.
func (g *Gateway) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	if !g.Identity.valid() || g.AccountPath == "" || g.ReceiptRoot == "" || g.LeaseExpiresAt.IsZero() || g.MaxBytes <= 0 || g.MaxBytes > MaxAttachmentBatchBytes || g.MaxEntries <= 0 || g.MaxEntries > MaxAttachmentEntries {
		return errors.New("exact attachment gateway is not fully authorized")
	}
	now := g.Now
	if now == nil {
		now = time.Now
	}
	if err := writeJSONLine(output, LeaseOffer{
		Version:          AttachmentProtocolVersion,
		Operation:        "lease_offer",
		NodeID:           g.Identity.NodeID,
		SessionID:        g.Identity.SessionID,
		ExpectedDeviceID: g.Identity.ExpectedDeviceID,
		SSHKeyID:         g.Identity.SSHKeyID,
		LeaseID:          g.Identity.LeaseID,
		ExpiresAt:        g.LeaseExpiresAt.UTC().Format(time.RFC3339Nano),
		MaxBytes:         g.MaxBytes,
		MaxEntries:       g.MaxEntries,
	}); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(input, MaxAttachmentHeaderBytes+1)
	first := true
	opened := false
	finished := false
	seen := make(map[string]bool)
	bytesTotal := int64(0)
	defer func() {
		// A lease is admitted before the first frame. EOF or a malformed open
		// must still invalidate that exact lease; cleanup is scoped to the
		// immutable identity and never adopts another session's files.
		if !finished {
			_ = g.cleanup(seen)
		}
	}()
	for {
		if now().After(g.LeaseExpiresAt) {
			return errors.New("exact attachment lease expired")
		}
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) && len(line) == 0 {
			return nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return errors.New("exact attachment frame read failed")
		}
		if len(line) == 0 || len(line) > MaxAttachmentHeaderBytes || line[len(line)-1] != '\n' {
			return errors.New("exact attachment frame is too large or incomplete")
		}
		var envelope map[string]json.RawMessage
		if err := rejectDuplicateKeys(line[:len(line)-1]); err != nil || json.Unmarshal(line[:len(line)-1], &envelope) != nil {
			return errors.New("invalid exact attachment frame")
		}
		var operation string
		if value, ok := envelope["operation"]; !ok || json.Unmarshal(value, &operation) != nil {
			return errors.New("invalid exact attachment operation")
		}
		switch operation {
		case "open":
			if !first || opened {
				return errors.New("exact attachment open must be first and unique")
			}
			var frame OpenFrame
			if err := decodeStrict(line[:len(line)-1], &frame); err != nil {
				return err
			}
			if err := frame.Validate(g.Identity); err != nil {
				return err
			}
			opened = true
			if err := writeJSONLine(output, map[string]any{"version": AttachmentProtocolVersion, "operation": "open_ack"}); err != nil {
				return err
			}
		case "upload":
			if !opened || first {
				return errors.New("exact attachment upload requires open")
			}
			var frame UploadFrame
			if err := decodeStrict(line[:len(line)-1], &frame); err != nil {
				return err
			}
			if err := frame.Validate(g.Identity, g.MaxBytes-bytesTotal, g.MaxEntries-len(seen)); err != nil {
				return err
			}
			if seen[frame.BatchID] {
				return errors.New("exact attachment batch replay")
			}
			if len(seen) >= g.MaxEntries {
				return errors.New("exact attachment entry limit exceeded")
			}
			path, err := g.receiveUpload(reader, frame)
			if err != nil {
				return err
			}
			seen[frame.BatchID] = true
			bytesTotal += frame.Size
			if err := writeJSONLine(output, map[string]any{"version": AttachmentProtocolVersion, "operation": "upload_ack", "batch_id": frame.BatchID, "path": path}); err != nil {
				return err
			}
		case "cleanup":
			if !opened || first {
				return errors.New("exact attachment cleanup requires open")
			}
			var frame CleanupFrame
			if err := decodeStrict(line[:len(line)-1], &frame); err != nil {
				return err
			}
			if err := frame.Validate(g.Identity); err != nil {
				return err
			}
			receipt := g.cleanup(seen)
			finished = true
			if err := writeJSONLine(output, receipt); err != nil {
				return err
			}
			opened = false
			return nil
		default:
			return errors.New("unsupported exact attachment operation")
		}
		first = false
		if errors.Is(err, io.EOF) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func (g *Gateway) receiveUpload(reader *bufio.Reader, frame UploadFrame) (string, error) {
	root := filepath.Join(g.AccountPath, ".agent-remote-attachments", g.Identity.SessionID, g.Identity.LeaseID, frame.BatchID)
	if err := ensurePrivateTree(root); err != nil {
		return "", err
	}
	path := filepath.Join(root, frame.Name)
	if filepath.Base(path) != frame.Name {
		return "", errors.New("attachment path escaped lease directory")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create attachment: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.CopyN(io.MultiWriter(file, hash), reader, frame.Size)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if copyErr != nil {
			return "", errors.New("attachment payload ended before declared size")
		}
		return "", closeErr
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != frame.SHA256 {
		_ = os.Remove(path)
		return "", errors.New("attachment checksum mismatch")
	}
	return path, nil
}

func (g *Gateway) cleanup(batches map[string]bool) CleanupReceipt {
	g.mu.Lock()
	defer g.mu.Unlock()
	status := CleanupCleaned
	removed := 0
	leaseRoot := filepath.Join(g.AccountPath, ".agent-remote-attachments", g.Identity.SessionID, g.Identity.LeaseID)
	if err := removeLeaseTree(leaseRoot); err != nil {
		status = CleanupPending
	} else {
		removed = len(batches)
	}
	receipt := CleanupReceipt{Version: AttachmentProtocolVersion, Operation: "cleanup_receipt", NodeID: g.Identity.NodeID, SessionID: g.Identity.SessionID, ExpectedDeviceID: g.Identity.ExpectedDeviceID, SSHKeyID: g.Identity.SSHKeyID, LeaseID: g.Identity.LeaseID, Status: status, RemovedBatches: removed, ReceiptID: newUUID()}
	if err := persistReceipt(g.ReceiptRoot, receipt); err != nil && status == CleanupCleaned {
		receipt.Status = CleanupPending
	}
	if g.CleanupHook != nil {
		if err := g.CleanupHook(receipt); err != nil {
			if receipt.Status != CleanupRejected {
				receipt.Status = CleanupUnknown
			}
			_ = persistReceipt(g.ReceiptRoot, receipt)
		}
	}
	return receipt
}

func removeLeaseTree(path string) error {
	if path == "" {
		return errors.New("empty lease path")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("lease path is not a directory")
	}
	return os.RemoveAll(path)
}

func persistReceipt(root string, receipt CleanupReceipt) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(root, 0o700)
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(root, ".cleanup-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(root, receipt.LeaseID+".json"))
}

func ensurePrivateTree(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	marker := string(filepath.Separator) + ".agent-remote-attachments"
	index := strings.Index(absolute, marker)
	if index < 0 {
		return errors.New("attachment path is outside the managed session root")
	}
	current := absolute[:index] + marker
	markerInfo, markerErr := os.Lstat(current)
	if markerErr != nil || !markerInfo.IsDir() || markerInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("attachment root is unsafe")
	}
	if err := os.Chmod(current, 0o700); err != nil {
		return err
	}
	parts := strings.Split(strings.TrimPrefix(absolute[index+len(marker):], string(filepath.Separator)), string(filepath.Separator))
	for _, part := range append([]string(nil), parts...) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return err
			}
			continue
		}
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("attachment path contains an unsafe directory")
		}
		if err := os.Chmod(current, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func validLeafName(name string) bool {
	return name != "" && len([]byte(name)) <= MaxAttachmentNameBytes && name != "." && name != ".." && !strings.ContainsAny(name, `/\\`) && !strings.ContainsRune(name, 0) && !strings.ContainsFunc(name, unicode.IsControl)
}

func decodeStrict(data []byte, out any) error {
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func writeJSONLine(out io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil || len(data) > MaxAttachmentHeaderBytes {
		return errors.New("attachment response is invalid")
	}
	_, err = out.Write(append(data, '\n'))
	return err
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := validateJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("attachment frame contains trailing data")
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("attachment frame object key is invalid")
			}
			if _, exists := seen[key]; exists {
				return errors.New("attachment frame contains a duplicate key")
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("attachment frame object is incomplete")
		}
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("attachment frame array is incomplete")
		}
	default:
		return errors.New("attachment frame delimiter is invalid")
	}
	return nil
}

func canonicalUUID(value string) bool {
	if len(value) != 36 || value == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func newUUID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "00000000-0000-4000-8000-000000000001"
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}

// NewLeaseID creates a canonical nonzero lease identifier for Server admission.
func NewLeaseID() string { return newUUID() }

// ResolveSessionAccount resolves a running session to the one managed account
// that already owns its prepared attachment directory. It never accepts an
// account path from a frame or command and rejects ambiguous mappings.
func ResolveSessionAccount(accountRoot, sessionID string) (string, error) {
	if !canonicalUUID(sessionID) || accountRoot == "" {
		return "", errors.New("invalid exact attachment session mapping")
	}
	root, err := filepath.Abs(accountRoot)
	if err != nil {
		return "", err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("managed account root is unavailable")
	}
	users, err := os.ReadDir(root)
	if err != nil {
		return "", errors.New("managed account root cannot be read")
	}
	var found string
	for _, user := range users {
		if !user.IsDir() || user.Type()&os.ModeSymlink != 0 {
			continue
		}
		claudeRoot := filepath.Join(root, user.Name(), "tool-accounts", "claude")
		claudeInfo, statErr := os.Lstat(claudeRoot)
		if statErr != nil || !claudeInfo.IsDir() || claudeInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}
		accounts, readErr := os.ReadDir(claudeRoot)
		if readErr != nil {
			return "", errors.New("managed Claude accounts cannot be read")
		}
		for _, account := range accounts {
			if !account.IsDir() || account.Type()&os.ModeSymlink != 0 {
				continue
			}
			candidate := filepath.Join(claudeRoot, account.Name())
			attachmentsRoot := filepath.Join(candidate, ".agent-remote-attachments")
			attachmentsInfo, attachmentsErr := os.Lstat(attachmentsRoot)
			if attachmentsErr != nil || !attachmentsInfo.IsDir() || attachmentsInfo.Mode()&os.ModeSymlink != 0 {
				continue
			}
			sessionPath := filepath.Join(attachmentsRoot, sessionID)
			info, statErr := os.Lstat(sessionPath)
			if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if found != "" && found != candidate {
				return "", errors.New("exact attachment session maps to multiple accounts")
			}
			found = candidate
		}
	}
	if found == "" {
		return "", errors.New("exact attachment session account is unavailable")
	}
	return found, nil
}
