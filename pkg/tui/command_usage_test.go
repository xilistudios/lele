package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommandUsageStore_RecordPersists pins the whole point of the store: the
// counts survive a restart, and a name recorded without its slash still lands
// on the same key.
func TestCommandUsageStore_RecordPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "command_usage.json")
	store := newCommandUsageStore(path)

	store.Record("/models")
	store.Record("/models")
	store.Record("/status")

	reloaded := newCommandUsageStore(path)
	if got := reloaded.Count("/models"); got != 2 {
		t.Errorf(`Count("/models") = %d, want 2`, got)
	}
	if got := reloaded.Count("/status"); got != 1 {
		t.Errorf(`Count("/status") = %d, want 1`, got)
	}
	if got := reloaded.Count("models"); got != 2 {
		t.Errorf(`Count("models") = %d, want 2 (normalisation without slash)`, got)
	}
	if got := reloaded.Count("/nope"); got != 0 {
		t.Errorf(`Count("/nope") = %d, want 0`, got)
	}
}

// TestCommandUsageStore_MissingFile makes sure a first run (no file yet) is not
// an error and that the first Record creates the file.
func TestCommandUsageStore_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "command_usage.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("precondition: %s should not exist (err=%v)", path, err)
	}

	store := newCommandUsageStore(path)
	if got := store.Count("/models"); got != 0 {
		t.Errorf("Count on a missing file = %d, want 0", got)
	}

	store.Record("/models")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Record should create %s: %v", path, err)
	}
	if got := newCommandUsageStore(path).Count("/models"); got != 1 {
		t.Errorf("Count after first Record = %d, want 1", got)
	}
}

// TestCommandUsageStore_MalformedFile keeps a corrupt file from breaking the
// TUI: the store starts empty, logs, and keeps recording.
func TestCommandUsageStore_MalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "command_usage.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("seeding malformed file: %v", err)
	}

	store := newCommandUsageStore(path)
	if got := store.Count("/models"); got != 0 {
		t.Errorf("Count on a malformed file = %d, want 0", got)
	}

	store.Record("/models")
	if got := newCommandUsageStore(path).Count("/models"); got != 1 {
		t.Errorf("Count after Record on a malformed file = %d, want 1", got)
	}
}

// TestCommandUsageStore_PrunesToMaxEntries pins the cap: the file cannot grow
// without bound, and the prune keeps the commands that were actually used.
func TestCommandUsageStore_PrunesToMaxEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "command_usage.json")
	store := newCommandUsageStore(path)

	for i := 0; i < commandUsageMaxEntries+50; i++ {
		store.Record(fmt.Sprintf("/cmd-%d", i))
	}
	// One frequent command must survive the prune even though it was recorded
	// after the map was already over the cap.
	for i := 0; i < 7; i++ {
		store.Record("/frequent")
	}

	reloaded := newCommandUsageStore(path)
	snapshot := reloaded.snapshot()
	if len(snapshot) > commandUsageMaxEntries {
		t.Errorf("persisted entries = %d, want at most %d", len(snapshot), commandUsageMaxEntries)
	}
	if got := reloaded.Count("/frequent"); got != 7 {
		t.Errorf(`Count("/frequent") = %d, want 7 (highest count must survive)`, got)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading persisted file: %v", err)
	}
	var file commandUsageFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("persisted file is not valid JSON: %v", err)
	}
	if len(file.Counts) > commandUsageMaxEntries {
		t.Errorf("file holds %d entries, want at most %d", len(file.Counts), commandUsageMaxEntries)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Error("persisted file should end with a newline")
	}
}

// TestFilterAutocomplete_OrdersByUsage pins the ranking itself, plus the
// invariant that reordering never drops or duplicates a command.
func TestFilterAutocomplete_OrdersByUsage(t *testing.T) {
	m := newTestModel(t)
	m.cmdUsage = newCommandUsageStore(filepath.Join(t.TempDir(), "usage.json"))

	m.cmdUsage.Record("/status")
	m.cmdUsage.Record("/status")
	m.cmdUsage.Record("/status")
	m.cmdUsage.Record("/clear")

	m.filterAutocomplete("/")
	names := autocompleteNames(m.autocompleteItems)

	idxStatus := indexOfName(names, "/status")
	idxClear := indexOfName(names, "/clear")
	idxSessions := indexOfName(names, "/sessions")
	if idxStatus < 0 || idxClear < 0 || idxSessions < 0 {
		t.Fatalf("expected /status, /clear and /sessions in the palette, got %v", names)
	}
	if !(idxStatus < idxClear && idxClear < idxSessions) {
		t.Errorf("usage order wrong: /status=%d, /clear=%d, /sessions=%d (want strictly increasing): %v",
			idxStatus, idxClear, idxSessions, names)
	}

	// Reordering must not lose or duplicate anything.
	if len(m.autocompleteItems) != len(allCommands) {
		t.Errorf("autocomplete items = %d, want %d (no duplicates/losses)",
			len(m.autocompleteItems), len(allCommands))
	}
	for _, cmd := range allCommands {
		if !containsName(names, cmd.name) {
			t.Errorf("builtin %s missing from the palette: %v", cmd.name, names)
		}
	}
}

// TestFilterAutocomplete_StableWithoutUsage is the backward-compatibility pin:
// a user with no history keeps today's declared order byte for byte.
func TestFilterAutocomplete_StableWithoutUsage(t *testing.T) {
	m := newTestModel(t)
	m.cmdUsage = newCommandUsageStore(filepath.Join(t.TempDir(), "usage.json"))

	m.filterAutocomplete("/")
	names := autocompleteNames(m.autocompleteItems)

	if len(names) != len(allCommands) {
		t.Fatalf("autocomplete items = %d, want %d", len(names), len(allCommands))
	}
	for i, cmd := range allCommands {
		if names[i] != cmd.name {
			t.Fatalf("position %d = %q, want %q (declared order): %v", i, names[i], cmd.name, names)
		}
	}
}

// TestExecuteCommand_RecordsUsage ties the counter to the real entry point:
// known commands are counted, unknown ones are not.
func TestExecuteCommand_RecordsUsage(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/quit")
	if got := m.cmdUsage.Count("/quit"); got != 1 {
		t.Errorf(`Count("/quit") = %d, want 1`, got)
	}

	m.executeCommand("/definitely-not-a-command")
	if got := m.cmdUsage.Count("/definitely-not-a-command"); got != 0 {
		t.Errorf(`unknown command was counted: Count = %d, want 0`, got)
	}
}

// indexOfName returns the position of want in names, or -1.
func indexOfName(names []string, want string) int {
	for i, n := range names {
		if n == want {
			return i
		}
	}
	return -1
}

// TestCommandUsageStore_SnapshotIsACopy pins that the ranking cannot be
// corrupted by a caller that mutates the map it was handed, and that the
// snapshot is taken under the lock (Count must not observe the mutation).
func TestCommandUsageStore_SnapshotIsACopy(t *testing.T) {
	store := newCommandUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	store.Record("/models")

	snapshot := store.snapshot()
	snapshot["/models"] = 99
	snapshot["/injected"] = 5

	if got := store.Count("/models"); got != 1 {
		t.Errorf(`Count("/models") = %d, want 1 (snapshot must be a copy)`, got)
	}
	if got := store.Count("/injected"); got != 0 {
		t.Errorf(`Count("/injected") = %d, want 0 (snapshot must be a copy)`, got)
	}
}

// TestCommandUsageStore_NilStoreIsSafe covers the `&Model{}` case: several TUI
// tests build a Model by hand, so the store must never be a precondition.
func TestCommandUsageStore_NilStoreIsSafe(t *testing.T) {
	var store *commandUsageStore
	store.Record("/models") // must not panic
	if got := store.Count("/models"); got != 0 {
		t.Errorf("Count on a nil store = %d, want 0", got)
	}
	if got := store.snapshot(); got != nil {
		t.Errorf("snapshot on a nil store = %v, want nil", got)
	}
	if err := store.save(); err != nil {
		t.Errorf("save on a nil store = %v, want nil", err)
	}
	sortCommandsByUsage([]commandInfo{{name: "/a"}, {name: "/b"}}, store) // must not panic
}

// TestFilterAutocomplete_CustomOutranksBuiltin completes the ranking rule: the
// source of a command (built-in vs harness) never decides its position, only
// its usage does.
func TestFilterAutocomplete_CustomOutranksBuiltin(t *testing.T) {
	m := harnessTestModel(t)
	m.cmdUsage = newCommandUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	m.cmdUsage.Record("/hola")
	m.cmdUsage.Record("/hola")
	m.cmdUsage.Record("/sessions")

	m.filterAutocomplete("/")
	names := autocompleteNames(m.autocompleteItems)

	if got := indexOfName(names, "/hola"); got != 0 {
		t.Errorf("/hola (2 uses) should lead the palette, got index %d: %v", got, names)
	}
	if got := indexOfName(names, "/sessions"); got != 1 {
		t.Errorf("/sessions (1 use) should come second, got index %d: %v", got, names)
	}
	// Everything else keeps the declared built-in order after the ranked ones.
	if got := indexOfName(names, "/new"); got != 2 {
		t.Errorf("/new (0 uses) should come third, got index %d: %v", got, names)
	}
}

// TestAllCommands_IncludesAddModel guards the palette entry added alongside the
// ranking: /add-model is a real command (executeCommand handles it) and must be
// discoverable, otherwise it can never be ranked either.
func TestAllCommands_IncludesAddModel(t *testing.T) {
	found := false
	for _, cmd := range allCommands {
		if cmd.name == "/add-model" {
			found = true
			if cmd.description == "" {
				t.Error("/add-model has an empty description")
			}
		}
	}
	if !found {
		t.Fatal("expected /add-model to be registered in allCommands")
	}
}
