package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync"
	"time"
)

// ErrConflict indicates that another operation changed the expected local record.
var ErrConflict = errors.New("task ledger record changed")

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
	failed  error
	publish func(string, []byte) (bool, error)
}

// Open loads or creates a ledger.
func Open(path string) (*Ledger, error) {
	ledger := &Ledger{path: path, entries: map[string]Entry{}, publish: publish}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ledger, nil
		}
		return nil, err
	}
	if err := decodeJSON(data, &ledger.entries); err != nil {
		return nil, err
	}
	if ledger.entries == nil {
		return nil, errors.New("ledger must contain a JSON object")
	}
	return ledger, nil
}

// Get returns an entry by task ID.
func (l *Ledger) Get(taskID string) (Entry, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failed != nil {
		return Entry{}, false, l.failed
	}
	entry, ok := l.entries[taskID]
	if !ok {
		return Entry{}, false, nil
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return Entry{}, false, err
	}
	var detached Entry
	if err := decodeJSON(data, &detached); err != nil {
		return Entry{}, false, err
	}
	return detached, true, nil
}

// Save stores an entry.
func (l *Ledger) Save(entry Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.saveLocked(entry)
}

// CompareAndSwap publishes only against the exact previously observed record; nil requires absence.
func (l *Ledger) CompareAndSwap(expected *Entry, entry Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failed != nil {
		return l.failed
	}
	current, exists := l.entries[entry.TaskID]
	if expected == nil {
		if exists {
			return ErrConflict
		}
	} else {
		if !exists || expected.TaskID != entry.TaskID {
			return ErrConflict
		}
		before, err := json.Marshal(expected)
		if err != nil {
			return err
		}
		actual, err := json.Marshal(current)
		if err != nil {
			return err
		}
		if !bytes.Equal(before, actual) {
			return ErrConflict
		}
	}
	return l.saveLocked(entry)
}

// List returns detached records of one status in logical task order.
func (l *Ledger) List(status string) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failed != nil {
		return nil, l.failed
	}
	entries := make([]Entry, 0)
	for _, entry := range l.entries {
		if entry.Status == status {
			entries = append(entries, entry)
		}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	var detached []Entry
	if err := decodeJSON(data, &detached); err != nil {
		return nil, err
	}
	sort.Slice(detached, func(i, j int) bool { return detached[i].TaskID < detached[j].TaskID })
	return detached, nil
}

func (l *Ledger) saveLocked(entry Entry) error {
	if l.failed != nil {
		return l.failed
	}
	entry.UpdatedAt = time.Now().UTC()
	next := make(map[string]Entry, len(l.entries)+1)
	for id, saved := range l.entries {
		next[id] = saved
	}
	next[entry.TaskID] = entry
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	// Decode before publication so callers cannot mutate the persisted result through map aliases.
	next = nil
	if err := decodeJSON(data, &next); err != nil {
		return err
	}
	renamed, err := l.publish(l.path, append(data, '\n'))
	if err != nil {
		if renamed {
			l.failed = errors.New("ledger publication is uncertain; reopen before executing tasks")
		}
		return err
	}
	l.entries = next
	return nil
}

func decodeJSON(data []byte, target any) error {
	if !json.Valid(data) {
		return errors.New("ledger must contain valid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	// Task metadata can contain int64 generations; float64 would change an exact replay.
	decoder.UseNumber()
	return decoder.Decode(target)
}
