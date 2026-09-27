package runtimehelper

import (
	"errors"
	"time"
)

type nativePaneExitWait struct {
	started time.Time
}

func (wait *nativePaneExitWait) inspect(output string, now time.Time) (bool, error) {
	if !wait.started.IsZero() && !now.Before(wait.started.Add(time.Second)) {
		return true, errors.New("managed pane exit status collection timed out")
	}
	if output == "1|" {
		// tmux can close the pty before collecting the process status. Absence is never success.
		if wait.started.IsZero() {
			wait.started = now
		}
		return false, nil
	} else if !wait.started.IsZero() && (output == "0|" || output == "0|0") {
		return false, errors.New("managed pane became live after terminal closure")
	}
	return parseNativePaneExit(output)
}
