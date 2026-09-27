package skillexport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type continuationAuthority struct {
	Authority
	renew func(context.Context, string) (skillmanager.NodeExportRenewal, error)
}

func (a continuationAuthority) RenewNodeExport(ctx context.Context, _, _, _, _, grant string) (skillmanager.NodeExportRenewal, error) {
	return a.renew(ctx, grant)
}

func successor(permission skillmanager.NodeExportPermission, previous, next string) skillmanager.NodeExportRenewal {
	digest := sha256.Sum256([]byte(previous))
	return skillmanager.NodeExportRenewal{PreviousGrantDigest: hex.EncodeToString(digest[:]), Grant: next, Permission: permission}
}

func TestContinuationOutlivesInitialGrantAndStillObservesRevocation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := frozenFixture(t)
		f.permission.RecheckSeconds = 10
		f.permission.ExpiresAt = time.Now().Add(2 * time.Second)
		var revoked atomic.Bool
		var calls atomic.Int32
		expected := exportGrant
		authority := continuationAuthority{Authority: f, renew: func(_ context.Context, grant string) (skillmanager.NodeExportRenewal, error) {
			if revoked.Load() || grant != expected {
				return skillmanager.NodeExportRenewal{}, ErrUnavailable
			}
			p := f.permission
			p.ExpiresAt = time.Now().Add(15 * time.Minute)
			expected = fmt.Sprintf("successor-%d=.%064x", calls.Add(1), 1)
			return successor(p, grant, expected), nil
		}}
		a := ownedAuthorization(t, f, authority, time.Now())
		defer a.close()
		time.Sleep(3 * time.Second)
		if !time.Now().After(f.permission.ExpiresAt) || a.check() != nil || calls.Load() == 0 {
			t.Fatal("valid continuation did not survive the original expiry")
		}
		revoked.Store(true)
		time.Sleep(11 * time.Second)
		if a.ctx.Err() == nil || a.check() == nil {
			t.Fatal("renewed credentials bypassed live revocation")
		}
	})
}

func TestContinuationCannotReplaceAuthorityOrReviveExpiredWindow(t *testing.T) {
	for _, fault := range []string{"predecessor", "grant", "binding", "facts", "denied"} {
		t.Run(fault, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := frozenFixture(t)
				f.permission.ExpiresAt = time.Now().Add(time.Minute)
				clean := false
				f.permission.IncomingDigest, f.permission.Unclean = &f.record.TreeDigest, &clean
				authority := continuationAuthority{Authority: f, renew: func(_ context.Context, grant string) (skillmanager.NodeExportRenewal, error) {
					p := f.permission
					p.ExpiresAt = time.Now().Add(15 * time.Minute)
					r := successor(p, grant, exportGrant)
					switch fault {
					case "predecessor":
						r.PreviousGrantDigest = fmt.Sprintf("%064x", 0)
					case "grant":
						r.Grant = "invalid"
					case "binding":
						r.Permission.Binding.DirectoryEpoch++
					case "facts":
						r.Permission.IncomingDigest, r.Permission.Unclean = nil, nil
					case "denied":
						return r, ErrUnavailable
					}
					return r, nil
				}}
				a := ownedAuthorization(t, f, authority, time.Now())
				defer a.close()
				if a.refresh() == nil || a.ctx.Err() == nil || a.refresh() == nil {
					t.Fatal("invalid continuation revived original authority")
				}
			})
		})
	}
}

func TestLateContinuationCannotReviveExpiredWindow(t *testing.T) {
	f := frozenFixture(t)
	authority := continuationAuthority{Authority: f, renew: func(_ context.Context, grant string) (skillmanager.NodeExportRenewal, error) {
		p := f.permission
		p.ExpiresAt = time.Now().Add(15 * time.Minute)
		// Real time avoids synctest's non-durable mutex wait blocking the expiry clock.
		time.Sleep(300 * time.Millisecond)
		return successor(p, grant, exportGrant), nil
	}}
	a := ownedAuthorization(t, f, authority, time.Now().Add(-800*time.Millisecond))
	if a.refresh() == nil || a.ctx.Err() == nil || a.check() == nil {
		t.Fatal("late successor revived an expired connection window")
	}
}

type slowExportConnection struct{ *exportConnection }

func (c slowExportConnection) Write(data []byte) (int, error) {
	time.Sleep(20 * time.Second)
	return c.exportConnection.Write(data)
}

type rotatingExportStore struct {
	*exportFixture
	latest *expiringExportReader
	opens  int
}

func (s *rotatingExportStore) OpenSkillFinalizationObjects(ctx context.Context, request string, record skillmanager.FinalizationRecord) (skillmanager.FrozenObjectReader, error) {
	if s.latest != nil && s.latest.closed {
		return nil, ErrUnavailable
	}
	if _, err := s.hold.Stat(); err != nil {
		return nil, err
	}
	r, err := s.exportFixture.OpenSkillFinalizationObjects(ctx, request, record)
	if err != nil {
		return nil, err
	}
	s.latest = &expiringExportReader{FrozenObjectReader: r, expires: time.Now().Add(15 * time.Minute)}
	s.opens++
	return s.latest, nil
}

type expiringExportReader struct {
	skillmanager.FrozenObjectReader
	expires time.Time
	closed  bool
}

func (r *expiringExportReader) Open(ctx context.Context, digest string) (*os.File, skillmanager.Entry, error) {
	if !time.Now().Before(r.expires) || r.closed {
		return nil, skillmanager.Entry{}, ErrUnavailable
	}
	return r.FrozenObjectReader.Open(ctx, digest)
}

func (r *expiringExportReader) Close() error {
	r.closed = true
	return r.FrozenObjectReader.Close()
}

func TestGatewayContinuesCompleteStreamAndRotatesOriginalReader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := frozenFixture(t)
		f.permission.RecheckSeconds = 10
		value := bytes.Repeat([]byte("x"), 2<<20)
		sum := sha256.Sum256(value)
		digest := hex.EncodeToString(sum[:])
		path := filepath.Join(t.TempDir(), "large")
		if err := os.WriteFile(path, value, 0600); err != nil {
			t.Fatal(err)
		}
		f.files[digest], f.bytes[digest] = path, value
		f.manifest.Entries[0].SHA256, f.manifest.Entries[0].Size = digest, int64(len(value))
		var err error
		f.record.TreeDigest, err = skillmanager.Digest(f.manifest)
		if err != nil {
			t.Fatal(err)
		}
		authority := continuationAuthority{Authority: f, renew: func(_ context.Context, grant string) (skillmanager.NodeExportRenewal, error) {
			p := f.permission
			p.ExpiresAt = time.Now().Add(15 * time.Minute)
			return successor(p, grant, exportGrant), nil
		}}
		store := &rotatingExportStore{exportFixture: f}
		stream := connection()
		started := time.Now()
		if err := Serve(context.Background(), slowExportConnection{stream}, f.identity(), authority, store); err != nil {
			t.Fatal("authorized progressing stream failed", err)
		}
		if time.Since(started) <= 15*time.Minute || store.opens < 2 || !store.latest.closed {
			t.Fatal("long stream did not rotate readers and close its final reader")
		}
		if _, err := f.hold.Stat(); err == nil {
			t.Fatal("completed stream leaked its outer immutable hold")
		}
		var out bytes.Buffer
		if err := RelaySnapshot(context.Background(), bytes.NewReader(stream.out.Bytes()), &out, func(header Header, _ bool) error {
			if header.TreeDigest != f.record.TreeDigest {
				return ErrUnavailable
			}
			return nil
		}); err != nil || !bytes.Equal(stream.out.Bytes(), out.Bytes()) {
			t.Fatal("long stream lacked complete verified objects and footer", err)
		}
	})
}

func TestOutputStallClosesTransportDespiteRenewableAuthority(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		left, right := net.Pipe()
		defer left.Close()
		defer right.Close()
		stop := context.AfterFunc(ctx, func() { _ = left.Close() })
		defer stop()
		started := time.Now()
		if _, err := NewIdleWriter(ctx, left, cancel).Write([]byte("blocked")); err == nil {
			t.Fatal("output stall remained authorized indefinitely")
		}
		if ctx.Err() == nil || time.Since(started) != WriteTimeout {
			t.Fatal("output did not close at its own progress deadline")
		}
	})
}

func TestHelperInputScanAndProgressAreIndependentlyBounded(t *testing.T) {
	for _, scan := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			left, right := net.Pipe()
			defer left.Close()
			defer right.Close()
			reader := &exportReader{input: left}
			expected := WriteTimeout
			if scan {
				reader.nextTimeout, expected = ScanTimeout, ScanTimeout
			}
			started := time.Now()
			var data [1]byte
			if _, err := reader.Read(data[:]); err == nil || time.Since(started) != expected {
				t.Fatal("Helper read did not retain its phase deadline")
			}
		})
	}
}
