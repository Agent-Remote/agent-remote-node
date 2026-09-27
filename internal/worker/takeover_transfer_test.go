package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type transferClientStub struct {
	takeoverLeaseFunc
	get      func(context.Context, skillmanager.AccountTakeoverBinding) (api.SkillTakeover, error)
	begin    func(context.Context, skillmanager.AccountCapture, skillmanager.Manifest) (api.SkillTakeover, error)
	put      func(context.Context, skillmanager.AccountTakeoverBinding, string, skillmanager.Entry, io.ReadCloser) error
	complete func(context.Context, skillmanager.AccountCapture, string) (api.SkillTakeover, error)
}

func (c transferClientStub) GetSkillTakeover(ctx context.Context, b skillmanager.AccountTakeoverBinding) (api.SkillTakeover, error) {
	return c.get(ctx, b)
}
func (c transferClientStub) BeginSkillTakeover(ctx context.Context, capture skillmanager.AccountCapture, m skillmanager.Manifest) (api.SkillTakeover, error) {
	return c.begin(ctx, capture, m)
}
func (c transferClientStub) PutSkillTakeoverFile(ctx context.Context, b skillmanager.AccountTakeoverBinding, id string, e skillmanager.Entry, r io.ReadCloser) error {
	return c.put(ctx, b, id, e, r)
}
func (c transferClientStub) CompleteSkillTakeover(ctx context.Context, capture skillmanager.AccountCapture, id string) (api.SkillTakeover, error) {
	return c.complete(ctx, capture, id)
}

type captureReaderStub struct {
	read func(context.Context, string, skillmanager.AccountTakeoverBinding) (skillmanager.AccountCapture, skillmanager.Manifest, error)
	open func(context.Context, string, skillmanager.AccountCapture, string) (*os.File, skillmanager.Entry, error)
}

func (h captureReaderStub) ReadAccountCapture(ctx context.Context, id string, b skillmanager.AccountTakeoverBinding) (skillmanager.AccountCapture, skillmanager.Manifest, error) {
	return h.read(ctx, id, b)
}
func (h captureReaderStub) OpenAccountCaptureObject(ctx context.Context, id string, c skillmanager.AccountCapture, d string) (*os.File, skillmanager.Entry, error) {
	return h.open(ctx, id, c, d)
}

type transferFixture struct {
	client   transferClientStub
	helper   captureReaderStub
	capture  skillmanager.AccountCapture
	manifest skillmanager.Manifest
	receipt  api.SkillTakeover
	events   []string
	handles  []*os.File
	path     string
}

func newTransferFixture(t *testing.T) *transferFixture {
	t.Helper()
	binding := skillmanager.AccountTakeoverBinding{Version: 1, NodeID: "11111111-1111-4111-8111-111111111111", UserID: "22222222-2222-4222-8222-222222222222", AccountID: "33333333-3333-4333-8333-333333333333", TakeoverID: "44444444-4444-4444-8444-444444444444", TaskID: "55555555-5555-4555-8555-555555555555", RuntimeBackend: "native", DirectoryEpoch: 3}
	binding.InventoryDigest, _ = skillmanager.AccountInventoryDigest([]skillmanager.AccountWriter{})
	content := []byte("retained bytes\n")
	sum := sha256.Sum256(content)
	entry := skillmanager.Entry{Path: "a", Kind: "file", Mode: 0644, Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:]), ContentKind: "text"}
	duplicate := entry
	duplicate.Path = "b"
	f := &transferFixture{manifest: skillmanager.Manifest{Version: 1, Entries: []skillmanager.Entry{entry, duplicate}}, path: filepath.Join(t.TempDir(), "retained")}
	if err := os.WriteFile(f.path, content, 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := skillmanager.Digest(f.manifest)
	if err != nil {
		t.Fatal(err)
	}
	f.capture = skillmanager.AccountCapture{Version: 1, Binding: binding, HelperReceiptID: "66666666-6666-4666-8666-666666666666", TreeDigest: digest, SourceExists: true}
	uploadID, checkpointID := "77777777-7777-4777-8777-777777777777", "88888888-8888-4888-8888-888888888888"
	f.receipt = api.SkillTakeover{Status: "committed", UploadID: &uploadID, CheckpointID: &checkpointID}
	f.client = transferClientStub{
		takeoverLeaseFunc: func(context.Context, skillmanager.AccountTakeoverBinding, int64) (api.SkillTakeoverLease, error) {
			return shortTakeoverLease(), nil
		},
		get: func(_ context.Context, b skillmanager.AccountTakeoverBinding) (api.SkillTakeover, error) {
			if b != binding {
				t.Error("wrong grant binding")
			}
			f.events = append(f.events, "get")
			return api.SkillTakeover{Status: "reserved"}, nil
		},
		begin: func(_ context.Context, c skillmanager.AccountCapture, m skillmanager.Manifest) (api.SkillTakeover, error) {
			if c != f.capture || !reflect.DeepEqual(m, f.manifest) {
				t.Error("capture changed")
			}
			f.events = append(f.events, "begin")
			return api.SkillTakeover{Status: "uploading", UploadID: &uploadID}, nil
		},
		put: func(_ context.Context, b skillmanager.AccountTakeoverBinding, id string, e skillmanager.Entry, r io.ReadCloser) error {
			defer r.Close()
			f.events = append(f.events, "put")
			if b != binding || id != uploadID {
				t.Error("wrong upload binding")
			}
			data, err := io.ReadAll(r)
			if err != nil {
				return err
			}
			return skillmanager.VerifyContent(e, data)
		},
		complete: func(_ context.Context, c skillmanager.AccountCapture, id string) (api.SkillTakeover, error) {
			if c != f.capture || id != uploadID {
				t.Error("wrong complete binding")
			}
			f.events = append(f.events, "complete")
			return f.receipt, nil
		},
	}
	f.helper = captureReaderStub{
		read: func(_ context.Context, _ string, b skillmanager.AccountTakeoverBinding) (skillmanager.AccountCapture, skillmanager.Manifest, error) {
			if b != binding {
				t.Error("wrong helper binding")
			}
			f.events = append(f.events, "read")
			return f.capture, f.manifest, nil
		},
		open: func(_ context.Context, _ string, c skillmanager.AccountCapture, d string) (*os.File, skillmanager.Entry, error) {
			if c != f.capture || d != entry.SHA256 {
				t.Error("wrong retained object")
			}
			f.events = append(f.events, "open")
			file, err := os.Open(f.path)
			if err == nil {
				f.handles = append(f.handles, file)
			}
			return file, entry, err
		},
	}
	t.Cleanup(func() {
		for _, file := range f.handles {
			if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Error("descriptor leaked")
				_ = file.Close()
			}
		}
		data, err := os.ReadFile(f.path)
		if err != nil || string(data) != string(content) {
			t.Error("retained capture was changed")
		}
	})
	return f
}
func (f *transferFixture) run(t *testing.T) (api.SkillTakeover, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return transferTakeoverCapture(ctx, f.client, f.helper, f.capture.Binding, 3)
}

func TestTakeoverTransferDeduplicatesRetainedFiles(t *testing.T) {
	f := newTransferFixture(t)
	result, err := f.run(t)
	if err != nil || result.CheckpointID != f.receipt.CheckpointID {
		t.Fatalf("transfer: %v", err)
	}
	if !reflect.DeepEqual(f.events, []string{"get", "read", "begin", "open", "put", "complete"}) {
		t.Fatalf("unexpected transfer order: %v", f.events)
	}
}
func TestTakeoverTransferCommittedReplaySkipsHelperAndLease(t *testing.T) {
	f := newTransferFixture(t)
	f.client.get = func(context.Context, skillmanager.AccountTakeoverBinding) (api.SkillTakeover, error) {
		return f.receipt, nil
	}
	f.client.takeoverLeaseFunc = nil
	f.helper = captureReaderStub{}
	result, err := f.run(t)
	if err != nil || result.CheckpointID != f.receipt.CheckpointID {
		t.Fatalf("replay: %v", err)
	}
}
func TestTakeoverTransferAuthorizationPrecedesHelper(t *testing.T) {
	for _, stage := range []string{"get", "lease"} {
		t.Run(stage, func(t *testing.T) {
			f := newTransferFixture(t)
			f.helper = captureReaderStub{}
			marker := errors.New("denied")
			if stage == "get" {
				f.client.get = func(context.Context, skillmanager.AccountTakeoverBinding) (api.SkillTakeover, error) {
					return api.SkillTakeover{}, marker
				}
			} else {
				f.client.takeoverLeaseFunc = func(context.Context, skillmanager.AccountTakeoverBinding, int64) (api.SkillTakeoverLease, error) {
					return api.SkillTakeoverLease{}, marker
				}
			}
			if _, err := f.run(t); err == nil {
				t.Fatal("unauthorized transfer succeeded")
			}
		})
	}
}
func TestTakeoverTransferFailuresRetainCapture(t *testing.T) {
	for _, stage := range []string{"read", "binding", "begin", "upload_id", "open", "entry", "put", "complete", "receipt"} {
		t.Run(stage, func(t *testing.T) {
			f := newTransferFixture(t)
			marker := errors.New("transfer failed")
			switch stage {
			case "read":
				f.helper.read = func(context.Context, string, skillmanager.AccountTakeoverBinding) (skillmanager.AccountCapture, skillmanager.Manifest, error) {
					return skillmanager.AccountCapture{}, skillmanager.Manifest{}, marker
				}
			case "binding":
				read := f.helper.read
				f.helper.read = func(ctx context.Context, id string, b skillmanager.AccountTakeoverBinding) (skillmanager.AccountCapture, skillmanager.Manifest, error) {
					c, m, err := read(ctx, id, b)
					c.Binding.DirectoryEpoch++
					return c, m, err
				}
			case "begin":
				f.client.begin = func(context.Context, skillmanager.AccountCapture, skillmanager.Manifest) (api.SkillTakeover, error) {
					return api.SkillTakeover{}, marker
				}
			case "upload_id":
				f.client.begin = func(context.Context, skillmanager.AccountCapture, skillmanager.Manifest) (api.SkillTakeover, error) {
					return api.SkillTakeover{Status: "uploading"}, nil
				}
			case "open":
				f.helper.open = func(context.Context, string, skillmanager.AccountCapture, string) (*os.File, skillmanager.Entry, error) {
					return nil, skillmanager.Entry{}, marker
				}
			case "entry":
				open := f.helper.open
				f.helper.open = func(ctx context.Context, id string, c skillmanager.AccountCapture, d string) (*os.File, skillmanager.Entry, error) {
					file, e, err := open(ctx, id, c, d)
					e.Mode = 0777
					return file, e, err
				}
			case "put":
				f.client.put = func(_ context.Context, _ skillmanager.AccountTakeoverBinding, _ string, _ skillmanager.Entry, r io.ReadCloser) error {
					defer r.Close()
					return marker
				}
			case "complete":
				f.client.complete = func(context.Context, skillmanager.AccountCapture, string) (api.SkillTakeover, error) {
					return api.SkillTakeover{}, marker
				}
			case "receipt":
				f.client.complete = func(context.Context, skillmanager.AccountCapture, string) (api.SkillTakeover, error) {
					return api.SkillTakeover{Status: "uploading"}, nil
				}
			}
			if _, err := f.run(t); err == nil {
				t.Fatal("invalid transfer succeeded")
			}
		})
	}
}
func TestTakeoverTransferLeaseLossCancelsUpload(t *testing.T) {
	f := newTransferFixture(t)
	var calls atomic.Int32
	f.client.takeoverLeaseFunc = func(context.Context, skillmanager.AccountTakeoverBinding, int64) (api.SkillTakeoverLease, error) {
		if calls.Add(1) > 1 {
			return api.SkillTakeoverLease{}, errors.New("denied")
		}
		return shortTakeoverLease(), nil
	}
	f.client.put = func(ctx context.Context, _ skillmanager.AccountTakeoverBinding, _ string, _ skillmanager.Entry, r io.ReadCloser) error {
		defer r.Close()
		<-ctx.Done()
		return ctx.Err()
	}
	_, err := f.run(t)
	if !errors.Is(err, errTakeoverLeaseLost) {
		t.Fatalf("lease loss: %v", err)
	}
	for _, event := range f.events {
		if event == "complete" {
			t.Fatal("completed after lease loss")
		}
	}
}
func TestTakeoverTransferCommitWinsTerminalRenewalRace(t *testing.T) {
	for _, stage := range []string{"begin", "complete"} {
		t.Run(stage, func(t *testing.T) {
			f := newTransferFixture(t)
			var calls atomic.Int32
			f.client.takeoverLeaseFunc = func(context.Context, skillmanager.AccountTakeoverBinding, int64) (api.SkillTakeoverLease, error) {
				if calls.Add(1) > 1 {
					return api.SkillTakeoverLease{}, errors.New("already committed")
				}
				return shortTakeoverLease(), nil
			}
			if stage == "begin" {
				f.client.begin = func(ctx context.Context, _ skillmanager.AccountCapture, _ skillmanager.Manifest) (api.SkillTakeover, error) {
					<-ctx.Done()
					return f.receipt, nil
				}
			} else {
				f.client.complete = func(ctx context.Context, _ skillmanager.AccountCapture, _ string) (api.SkillTakeover, error) {
					<-ctx.Done()
					return f.receipt, nil
				}
			}
			result, err := f.run(t)
			if err != nil || result.CheckpointID != f.receipt.CheckpointID {
				t.Fatalf("verified commit lost to renewal: %v", err)
			}
		})
	}
}
