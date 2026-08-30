package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestSaveConfig builds a Config with a couple of distinctive,
// env-var-free values so LoadConfig round-trips them unchanged.
func newTestSaveConfig() *Config {
	cfg := DefaultConfig()
	cfg.Language = "pt"
	cfg.Gateway = GatewayConfig{Host: "127.0.0.1", Port: 9911}
	cfg.Agents.List = []AgentConfig{{ID: "atomic-check", Name: "Atomic"}}
	return cfg
}

func TestSaveConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := SaveConfig(path, newTestSaveConfig()); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if loaded.Language != "pt" {
		t.Errorf("Language = %q, want %q", loaded.Language, "pt")
	}
	if loaded.Gateway.Port != 9911 {
		t.Errorf("Gateway.Port = %d, want 9911", loaded.Gateway.Port)
	}
	if len(loaded.Agents.List) != 1 || loaded.Agents.List[0].ID != "atomic-check" {
		t.Errorf("Agents.List round-trip mismatch: %+v", loaded.Agents.List)
	}

	// Atomic rename must still leave the file at mode 0600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("config file has permission %04o, want 0600", perm)
	}
}

func TestSaveConfigNoTempLeftovers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := SaveConfig(path, newTestSaveConfig()); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	leftovers, err := filepath.Glob(filepath.Join(dir, ".lele-config-*"))
	if err != nil {
		t.Fatalf("Glob failed: %v", err)
	}
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind after successful save: %v", leftovers)
	}
}

// TestSaveConfigFailureKeepsOldFile injects a deterministic failure in the
// final os.Rename step: the target path is pre-created as a DIRECTORY, and
// renaming a regular file onto a directory fails with ENOTDIR/EEXIST on Linux
// (verified: "rename ...: file exists" for both empty and non-empty dirs).
// This exercises the post-CreateTemp error path, so we also assert the temp
// file is cleaned up and the old config content is untouched.
func TestSaveConfigFailureKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Step 1: failure after temp creation (rename onto a directory).
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatalf("mkdir target failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "inside"), []byte("keep"), 0600); err != nil {
		t.Fatalf("write inside failed: %v", err)
	}

	err := SaveConfig(path, newTestSaveConfig())
	if err == nil {
		t.Fatal("SaveConfig succeeded renaming file onto directory; want error")
	}

	leftovers, gerr := filepath.Glob(filepath.Join(dir, ".lele-config-*"))
	if gerr != nil {
		t.Fatalf("Glob failed: %v", gerr)
	}
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind after failed save: %v", leftovers)
	}

	// Step 2: with a valid old file present, a later failure must not alter it.
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("RemoveAll failed: %v", err)
	}
	if err := SaveConfig(path, newTestSaveConfig()); err != nil {
		t.Fatalf("baseline SaveConfig failed: %v", err)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	// Make the whole dir read-only after the file exists: MkdirAll on an
	// existing dir succeeds, but CreateTemp then fails with permission
	// denied, so nothing can touch the old file.
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("chmod failed: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0755) })

	bad := filepath.Join(dir, "sub", "config.json")
	if err := SaveConfig(bad, newTestSaveConfig()); err == nil {
		t.Fatal("SaveConfig into read-only dir succeeded; want error")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after failure failed: %v", err)
	}
	if string(after) != string(old) {
		t.Error("old config file changed after failed save")
	}
	leftovers, gerr = filepath.Glob(filepath.Join(dir, ".lele-config-*"))
	if gerr != nil {
		t.Fatalf("Glob failed: %v", gerr)
	}
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind after failed save: %v", leftovers)
	}
}

// TestSaveConfigConcurrentReaderNeverSeesPartial runs a background reader
// looping LoadConfig while the main goroutine loops SaveConfig. With the old
// direct os.WriteFile, the reader could observe truncated JSON ("unexpected
// end of JSON input"); with tmp+rename the file is swapped atomically, so the
// reader must never see a JSON syntax error.
func TestSaveConfigConcurrentReaderNeverSeesPartial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := SaveConfig(path, newTestSaveConfig()); err != nil {
		t.Fatalf("seed SaveConfig failed: %v", err)
	}

	const iterations = 200
	var (
		stop    atomic.Bool
		wg      sync.WaitGroup
		partial atomic.Value // stores first error string
		readers = 2
	)

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_, err := LoadConfig(path)
				var syn *json.SyntaxError
				if err != nil && errors.As(err, &syn) {
					partial.CompareAndSwap(nil, err.Error())
				}
				// Yield the hot read loop briefly: with the old direct
				// WriteFile a partial file persisted for the whole write,
				// so even a sampled reader reliably observes corruption,
				// while here it keeps I/O contention (and runtime) down.
				time.Sleep(time.Millisecond)
			}
		}()
	}

	for i := 0; i < iterations; i++ {
		cfg := newTestSaveConfig()
		cfg.Gateway.Port = 9000 + i // vary content so each write differs
		if err := SaveConfig(path, cfg); err != nil {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("SaveConfig iteration %d failed: %v", i, err)
		}
	}
	stop.Store(true)
	wg.Wait()

	if v := partial.Load(); v != nil {
		t.Fatalf("reader observed partial config: %v", v)
	}

	// No temp leftovers after the hammering either.
	leftovers, err := filepath.Glob(filepath.Join(dir, ".lele-config-*"))
	if err != nil {
		t.Fatalf("Glob failed: %v", err)
	}
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}

	// Final file must be valid JSON with the last written port.
	final, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("final LoadConfig failed: %v", err)
	}
	if final.Gateway.Port != 9000+iterations-1 {
		t.Errorf("final Gateway.Port = %d, want %d", final.Gateway.Port, 9000+iterations-1)
	}
}
