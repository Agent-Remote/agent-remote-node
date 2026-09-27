package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestPreparationClientsKeepCapacityBudgetAndEarlierCancellation(t *testing.T) {
	for _, deployment := range []bool{false, true} {
		for _, earlier := range []bool{false, true} {
			name := "snapshot"
			if deployment {
				name = "deployment"
			}
			if earlier {
				name += "_caller_deadline"
			}
			t.Run(name, func(t *testing.T) {
				content := []byte("original")
				client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
					var digest string
					var reader io.Reader
					if deployment {
						input, buffered := readDeploymentTestInput(t, connection)
						digest, reader = input.Manifest.Entries[1].SHA256, buffered
					} else {
						buffered, input := readPreparationTestInput(t, connection)
						digest, reader = input.Manifest.Entries[0].SHA256, buffered
					}
					_ = json.NewEncoder(connection).Encode(skillPreparationFrame{Version: 1, Kind: "object", Digest: digest})
					_, _ = io.Copy(io.Discard, reader)
				})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var expected time.Time
				if earlier {
					expected = time.Now().Add(300 * time.Millisecond)
					var stop context.CancelFunc
					ctx, stop = context.WithDeadline(ctx, expected)
					defer stop()
				}
				called := false
				download := func(work context.Context, _ skillmanager.Entry, _ io.Writer) error {
					called = true
					deadline, ok := work.Deadline()
					if !ok {
						t.Error("preparation has no absolute deadline")
					} else if earlier {
						if !deadline.Equal(expected) {
							t.Error("preparation replaced the caller deadline")
						}
					} else {
						remaining := time.Until(deadline)
						if remaining < 2*time.Hour || remaining > 3*time.Hour {
							t.Error("default-capacity preparation lacks its bounded transfer budget")
						}
						cancel()
					}
					<-work.Done()
					return work.Err()
				}
				var err error
				if deployment {
					_, err = client.PrepareSkillDeployment(ctx, "capacity-budget", preparationTestDeployment(t, content), download)
				} else {
					err = client.PrepareSkillSnapshot(ctx, "capacity-budget", preparationTestSnapshot(t, content), download)
				}
				want := context.Canceled
				if earlier {
					want = context.DeadlineExceeded
				}
				if !called || !errors.Is(err, want) {
					t.Fatal("preparation lost download cancellation", err)
				}
			})
		}
	}
}

func TestPreparationServerBoundsWorkAndCancelsBlockedInput(t *testing.T) {
	for _, earlier := range []bool{false, true} {
		name := "capacity_budget"
		if earlier {
			name = "caller_deadline"
		}
		t.Run(name, func(t *testing.T) {
			server, peer := net.Pipe()
			defer peer.Close()
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			var expected time.Time
			if earlier {
				expected = time.Now().Add(100 * time.Millisecond)
				var stop context.CancelFunc
				parent, stop = context.WithDeadline(parent, expected)
				defer stop()
			}
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				withSkillPreparationStream(parent, server, server, func(work context.Context, input io.Reader) {
					deadline, ok := work.Deadline()
					if !ok || earlier && !deadline.Equal(expected) || !earlier && (time.Until(deadline) < 2*time.Hour || time.Until(deadline) > 3*time.Hour) {
						t.Error("Helper preparation has an incorrect bounded deadline")
					}
					if !earlier {
						cancel()
					}
					if _, err := io.ReadAll(input); err == nil {
						t.Error("cancelled stream acknowledged incomplete input")
					}
				})
			}()
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("cancelled Helper retained its blocked reader")
			}
		})
	}
}
