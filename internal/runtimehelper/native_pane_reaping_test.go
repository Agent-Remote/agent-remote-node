package runtimehelper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeReapingRequiresOriginalPaneStatus(t *testing.T) {
	for _, status := range []string{"0", "7", ""} {
		t.Run("status_"+status, func(t *testing.T) {
			root := t.TempDir()
			wake := filepath.Join(root, "wake")
			calls := filepath.Join(root, "calls")
			// Only a new child event makes the retained original status visible in this fixture.
			script := "shift 2\ncase \"$1\" in\n" +
				"display-message) if [ -f " + shellQuote(wake) + " ]; then printf '1|" + status + "\\n'; else printf '1|\\n'; fi ;;\n" +
				"run-shell) test \"$2\" = -b && test \"$3\" = -t && test \"$4\" = %1 && test \"$5\" = /bin/true || exit 1\n" +
				"touch " + shellQuote(wake) + "; printf 'wake\\n' >> " + shellQuote(calls) + " ;;\n" +
				"kill-session) exit 0 ;;\n*) exit 1 ;;\nesac\n"
			command := writeTestCommand(t, "tmux", script)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := waitManagedNativePane(ctx, EngineConfig{TmuxBinaryPath: command}, SessionSpec{TmuxSocketPath: "unused", TmuxSessionName: "original"}, "%1", nil)
			if (err == nil) != (status == "0") {
				t.Fatal("reaping command substituted for actual original pane success")
			}
			data, readErr := os.ReadFile(calls)
			count := strings.Count(string(data), "wake\n")
			if readErr != nil || count < 1 || count > 5 {
				t.Fatal("pane reaping requests were absent or exceeded their bound")
			}
		})
	}
}
