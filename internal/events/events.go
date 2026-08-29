// Package events provides the in-process event bus used to push live updates
// to browser clients (over WebSocket) and the persisted activity/audit log.
//
// Events describe high-level application actions only ("connected",
// "upload completed", "auth failed"). They never contain passwords, private
// keys, passphrases, raw file contents, or full credentials.
package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Type enumerates event kinds.
type Type string

const (
	TypeEngineReady     Type = "engine.ready"
	TypeConnectionState Type = "connection.state" // connected/disconnected/error
	TypeDirectoryListed Type = "directory.listed"
	TypeTransferUpdate  Type = "transfer.update"
	TypeTransferAdded   Type = "transfer.added"
	TypePreviewOpened   Type = "preview.opened"
	TypeAuthFailed      Type = "auth.failed"
	TypeHostKeyPrompt   Type = "hostkey.prompt"
	TypeInfo            Type = "info"
	TypeWarning         Type = "warning"
	TypeError           Type = "error"
	TypeSettings        Type = "settings.changed"
)

// Event is one bus message.
type Event struct {
	Type      Type        `json:"type"`
	Time      time.Time   `json:"time"`
	Message   string      `json:"message,omitempty"`
	ConnID    string      `json:"connectionId,omitempty"`
	SessionID string      `json:"sessionId,omitempty"`
	Data      interface{} `json:"data,omitempty"`
}

// ActivityEntry is the persisted, sanitized audit record.
type ActivityEntry struct {
	ID        int       `json:"id"`
	Time      time.Time `json:"time"`
	Level     string    `json:"level"` // info | warning | error
	Type      Type      `json:"type"`
	Message   string    `json:"message"`
	ConnName  string    `json:"connectionName,omitempty"`
	SessionID string    `json:"sessionId,omitempty"`
}

const maxActivity = 500

// Bus fans events out to subscribers and appends sanitized entries to the
// on-disk activity log.
type Bus struct {
	mu           sync.RWMutex
	subs         map[chan Event]struct{}
	activity     []ActivityEntry
	nextID       int
	activityPath string
	maxLevel     string
}

// NewBus creates a bus. logLevel is "off"|"error"|"warning"|"info"|"debug".
func NewBus(dataDir, logLevel string) *Bus {
	b := &Bus{
		subs:         map[chan Event]struct{}{},
		activityPath: filepath.Join(dataDir, "activity.log.json"),
		maxLevel:     logLevel,
	}
	b.loadActivity()
	return b
}

func levelRank(l string) int {
	switch l {
	case "off":
		return -1
	case "error":
		return 0
	case "warning":
		return 1
	case "info", "debug", "":
		return 2
	}
	return 2
}

func (b *Bus) allows(level string) bool {
	return levelRank(b.maxLevel) >= levelRank(level)
}

// SetLevel changes the log level at runtime.
func (b *Bus) SetLevel(l string) {
	b.mu.Lock()
	b.maxLevel = l
	b.mu.Unlock()
}

// Subscribe returns a buffered channel of events and an unsubscribe func.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 128)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
		close(ch)
	}
}

// Emit publishes an event to all subscribers and records it in the activity
// log when appropriate.
func (b *Bus) Emit(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	b.mu.RLock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
			// Slow consumer: drop non-critical update rather than block.
		}
	}
	b.mu.RUnlock()

	level := "info"
	switch e.Type {
	case TypeError, TypeAuthFailed:
		level = "error"
	case TypeWarning, TypeHostKeyPrompt:
		level = "warning"
	}
	// High-frequency transfer updates are not persisted to the audit log.
	persist := e.Type != TypeTransferUpdate
	if persist && b.allows(level) {
		b.record(ActivityEntry{
			Time:      e.Time,
			Level:     level,
			Type:      e.Type,
			Message:   e.Message,
			SessionID: e.SessionID,
		})
	}
}

// Convenience helpers.
func (b *Bus) Info(t Type, msg string, kv ...pair) { b.Emit(build(t, msg, kv...)) }
func (b *Bus) Warn(msg string, kv ...pair)         { b.Emit(build(TypeWarning, msg, kv...)) }
func (b *Bus) Error(msg string, kv ...pair)        { b.Emit(build(TypeError, msg, kv...)) }

type pair struct{ k, v string }

// P builds a string key/value pair for messages.
func P(k, v string) pair { return pair{k, v} }

func build(t Type, msg string, kv ...pair) Event {
	e := Event{Type: t, Message: msg}
	for _, p := range kv {
		switch p.k {
		case "conn":
			e.ConnID = p.v
		case "session":
			e.SessionID = p.v
		}
	}
	return e
}

// ---- activity log persistence ------------------------------------------

func (b *Bus) record(a ActivityEntry) {
	b.mu.Lock()
	b.nextID++
	a.ID = b.nextID
	b.activity = append(b.activity, a)
	if len(b.activity) > maxActivity {
		b.activity = b.activity[len(b.activity)-maxActivity:]
	}
	b.mu.Unlock()
	b.persistActivity()
}

// Activity returns the stored entries, newest first.
func (b *Bus) Activity() []ActivityEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]ActivityEntry, len(b.activity))
	copy(out, b.activity)
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// ClearActivity wipes the local activity log.
func (b *Bus) ClearActivity() error {
	b.mu.Lock()
	b.activity = nil
	b.nextID = 0
	b.mu.Unlock()
	if err := os.Remove(b.activityPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (b *Bus) loadActivity() {
	raw, err := os.ReadFile(b.activityPath)
	if err != nil {
		return
	}
	var doc struct {
		Entries []ActivityEntry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return
	}
	b.activity = doc.Entries
	if len(b.activity) > 0 {
		b.nextID = b.activity[len(b.activity)-1].ID
	}
}

func (b *Bus) persistActivity() {
	b.mu.RLock()
	entries := make([]ActivityEntry, len(b.activity))
	copy(entries, b.activity)
	b.mu.RUnlock()
	doc := struct {
		Entries []ActivityEntry `json:"entries"`
	}{Entries: entries}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return
	}
	tmp := b.activityPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, b.activityPath)
}
