package ledger

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry records local task execution state.
type Entry struct {
	TaskID    string         `json:"task_id"`
	Status    string         `json:"status"`
	Result    map[string]any `json:"result,omitempty"`
	Error     map[string]any `json:"error,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// Ledger stores task results on local disk.
type Ledger struct {
	path    string
	mu      sync.Mutex
	entries map[string]Entry
}

// Open loads or creates a ledger.
func Open(path string) (*Ledger, error) {
	ledger := &Ledger{path: path, entries: map[string]Entry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ledger, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return ledger, nil
	}
	if err := json.Unmarshal(data, &ledger.entries); err != nil {
		return nil, err
	}
	return ledger, nil
}

// Get returns an entry by task ID.
func (l *Ledger) Get(taskID string) (Entry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[taskID]
	return entry, ok
}

// Save stores an entry.
func (l *Ledger) Save(entry Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry.UpdatedAt = time.Now().UTC()
	l.entries[entry.TaskID] = entry
	return l.flushLocked()
}

func (l *Ledger) flushLocked() error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil && filepath.Dir(l.path) != "." {
		return err
	}
	data, err := json.MarshalIndent(l.entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(l.path, append(data, '\n'), 0o600)
}
