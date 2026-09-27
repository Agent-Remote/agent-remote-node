package skillexport

import (
	"context"
	"testing"
	"time"
)

func TestPendingCaptureExportRetainsClassificationWithoutDigest(t *testing.T) {
	for _, fault := range []string{"", "forgotten", "changed", "matching_digest", "wrong_digest"} {
		t.Run(fault, func(t *testing.T) {
			f := frozenFixture(t)
			unclean := f.record.Unclean
			f.permission.Unclean = &unclean
			if err := f.permission.MatchExport(f.permission.Binding, f.record.TreeDigest, unclean); err != nil {
				t.Fatal(err)
			}
			if err := f.permission.MatchExport(f.permission.Binding, f.record.TreeDigest, !unclean); err == nil {
				t.Fatal("classification was not enforced")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			authorization := exportAuthorization{ctx: ctx, cancel: cancel, identity: f.identity(), grant: exportGrant, initial: f.permission, current: f.permission, authority: f}
			authorization.setWindow(time.Now(), f.permission)
			if err := authorization.observe(Header{Binding: f.permission.Binding, TreeDigest: f.record.TreeDigest, Unclean: unclean}, true); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "forgotten":
				f.permission.Unclean = nil
			case "changed":
				other := !unclean
				f.permission.Unclean = &other
			case "matching_digest":
				f.permission.IncomingDigest = &f.record.TreeDigest
			case "wrong_digest":
				f.permission.IncomingDigest = &f.permission.Binding.InitialTreeDigest
			}
			err := authorization.refresh()
			if (err != nil) != (fault == "forgotten" || fault == "changed" || fault == "wrong_digest") {
				t.Fatal("non-monotonic export authority", fault, err)
			}
		})
	}
}
