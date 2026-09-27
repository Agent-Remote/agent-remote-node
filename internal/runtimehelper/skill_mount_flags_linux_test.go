package runtimehelper

import (
	"strings"
	"testing"
)

func TestManagedLaunchMountFlags(t *testing.T) {
	valid := "123 100 0:1 /work /session/skill\\040work rw,nosuid,nodev,relatime - ext4 /dev/test rw\n"
	for _, name := range []string{"valid", "missing", "readonly", "suid", "devices", "shared", "slave", "duplicate", "malformed", "truncated"} {
		t.Run(name, func(t *testing.T) {
			input := valid
			switch name {
			case "missing":
				input = strings.Replace(valid, "123 ", "124 ", 1)
			case "readonly":
				input = strings.Replace(valid, "rw,", "ro,", 1)
			case "suid":
				input = strings.Replace(valid, ",nosuid", "", 1)
			case "devices":
				input = strings.Replace(valid, ",nodev", "", 1)
			case "shared":
				input = strings.Replace(valid, " - ", " shared:15 - ", 1)
			case "slave":
				input = strings.Replace(valid, " - ", " master:15 - ", 1)
			case "duplicate":
				input += valid
			case "malformed":
				input = "123 100\n"
			case "truncated":
				input += strings.Repeat("1 2 3\n", 2<<20)
			}
			if err := verifySkillMountInfo(strings.NewReader(input), 123); (err == nil) != (name == "valid") {
				t.Fatalf("unexpected mount verification: %v", err)
			}
		})
	}
}
