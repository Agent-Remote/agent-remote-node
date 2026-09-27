package runtimehelper

import (
	"testing"
	"time"
)

func TestNativePendingPaneExitNeedsFinalStatus(t *testing.T) {
	start := time.Unix(100, 0)
	for _, tc := range []struct {
		name, final  string
		delay        time.Duration
		dead, failed bool
	}{
		{"collected_zero", "1|0", 200 * time.Millisecond, true, false},
		{"collected_failure", "1|7", 200 * time.Millisecond, true, true},
		{"still_pending", "1|", 900 * time.Millisecond, false, false},
		{"unknown_deadline", "1|", time.Second, true, true},
		{"late_zero", "1|0", time.Second, true, true},
		{"revived_pane", "0|", 200 * time.Millisecond, false, true},
		{"malformed", "1|+0", 200 * time.Millisecond, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var wait nativePaneExitWait
			if dead, err := wait.inspect("1|", start); dead || err != nil {
				t.Fatal("initial pending status was treated as final")
			}
			dead, err := wait.inspect(tc.final, start.Add(tc.delay))
			if dead != tc.dead || (err != nil) != tc.failed {
				t.Fatal("pending pane observation accepted an invalid outcome")
			}
			if wait.started != start {
				t.Fatal("later observation extended the original deadline")
			}
		})
	}
}
