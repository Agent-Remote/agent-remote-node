package runtimehelper

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestHelperFinalizationRebootMountInventory(t *testing.T) {
	const base = "1 0 8:1 / / rw - ext4 /dev/root rw\n2 1 8:2 /subvolume /skills rw shared:7 - btrfs /dev/data rw\n"
	for _, test := range []struct {
		name, extra, target string
		valid               bool
	}{
		{"absent", "", "/runtime/session", true},
		{"separate_volume", "", "/skills/session/work", true},
		{"sibling", "3 1 8:1 /other /runtime/session-other rw - ext4 /dev/root rw\n", "/runtime/session", true},
		{"mounted_root", "3 1 8:3 / /runtime/session rw - tmpfs tmpfs rw\n", "/runtime/session", false},
		{"descendant", "3 1 8:3 / /runtime/session/tmp/nested rw - tmpfs tmpfs rw\n", "/runtime/session", false},
		{"bind_alias", "3 1 8:2 /subvolume/session/work /exposed rw - btrfs /dev/data rw\n", "/skills/session/work", false},
		{"child_alias", "3 1 8:2 /subvolume/session/work/db /exposed rw - btrfs /dev/data rw\n", "/skills/session/work", false},
		{"parent_alias", "3 1 8:2 /subvolume/session /exposed rw - btrfs /dev/data rw\n", "/skills/session/work", false},
		{"different_filesystem", "3 1 8:3 /subvolume/session/work /exposed rw - ext4 /dev/other rw\n", "/skills/session/work", true},
		{"escaped_space", "3 1 8:2 /subvolume/session\\040one/work /exposed rw - btrfs /dev/data rw\n", "/skills/session one/work", false},
		{"bad_escape", "3 1 8:2 /bad\\001 /exposed rw - btrfs /dev/data rw\n", "/skills/session/work", false},
		{"duplicate", "2 1 8:2 /else /other rw - btrfs /dev/data rw\n", "/skills/session/work", false},
		{"truncated", "3 1 8:2 / /other rw - btrfs\n", "/skills/session/work", false},
		{"malformed_device", "3 1 unknown / /other rw - btrfs /dev/data rw\n", "/skills/session/work", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := verifyNoRebootMounts(context.Background(), strings.NewReader(base+test.extra), test.target)
			if (err == nil) != test.valid {
				t.Fatal("unexpected mount absence proof", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := verifyNoRebootMounts(ctx, strings.NewReader(base), "/runtime/session"); !errors.Is(err, context.Canceled) {
		t.Fatal("mount inventory ignored cancellation", err)
	}
	for _, input := range []string{"", strings.Repeat("x", 8<<20+1)} {
		if err := verifyNoRebootMounts(context.Background(), strings.NewReader(input), "/runtime/session"); err == nil {
			t.Fatal("empty or unbounded mount inventory accepted")
		}
	}
}
