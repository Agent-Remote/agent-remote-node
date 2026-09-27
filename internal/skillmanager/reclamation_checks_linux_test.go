package skillmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestReclamationChecksBoundExternalInspectionAndGuardEveryUnlink(t *testing.T) {
	bundle, _, binding := sealedBundle(t)
	for i := range 128 {
		if err := bundle.WriteFile(fmt.Sprintf("work/entry-%03d", i), []byte("same object"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, capture, authority := freezeReclamationBundle(t, bundle, binding)
	intent := markReclamation(t, bundle, capture, authority)
	verified, guarded := 0, 0
	checks := ReclamationChecks{
		Verify: func(ctx context.Context) error { verified++; return ctx.Err() },
		Guard:  func(ctx context.Context) error { guarded++; return ctx.Err() },
	}
	if err := ReclaimFinalizationContentWithChecks(context.Background(), bundle, intent, checks); err != nil {
		t.Fatal(err)
	}
	if verified > 8 || verified < 6 || guarded < 129 {
		t.Fatalf("external proof should be bounded while every entry is guarded: verify=%d guard=%d", verified, guarded)
	}
}

func TestReclamationChecksPreserveInterruptedPhaseForResume(t *testing.T) {
	for _, phase := range []string{"preflight", "first_unlink", "after_work"} {
		t.Run(phase, func(t *testing.T) {
			bundle, capture, authority := reclamationBundle(t)
			intent := markReclamation(t, bundle, capture, authority)
			blocked := errors.New("resource reappeared")
			checks := ReclamationChecks{
				Verify: func(ctx context.Context) error {
					if phase == "preflight" {
						return blocked
					}
					if _, err := bundle.Lstat("work"); phase == "after_work" && errors.Is(err, os.ErrNotExist) {
						return blocked
					}
					return ctx.Err()
				},
				Guard: func(ctx context.Context) error {
					if phase == "first_unlink" {
						return blocked
					}
					return ctx.Err()
				},
			}
			if err := ReclaimFinalizationContentWithChecks(context.Background(), bundle, intent, checks); !errors.Is(err, blocked) {
				t.Fatal("phase failure did not stop deletion", err)
			}
			if _, complete, err := ReadFinalizationReclamation(bundle, capture.Binding); err != nil || complete {
				t.Fatal("phase failure completed reclamation", err)
			}
			if _, err := bundle.Lstat("finalization/objects"); err != nil {
				t.Fatal("phase failure removed later object root", err)
			}
			if err := ReclaimFinalizationContent(context.Background(), bundle, intent, reclamationQuiescent); err != nil {
				t.Fatal("phase failure lost restart", err)
			}
		})
	}
}
