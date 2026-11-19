// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/xilistudios/lele/pkg/logger"
)

// usageStore records how often each remote MCP tool has been called, per
// server: counts[server][remoteTool] is the number of successful remote calls
// made so far.
//
// WHY this exists: the lazy tool-discovery flow (a list_mcp_tools tool) has to
// answer "which of this server's tools does this user actually use?" long
// before the tool schemas are loaded — that answer is a frequency ranking, so
// the counts are kept here, next to the call itself (Manager.CallRemote), and
// never derived from the (short-lived, per-session) loaded set.
//
// Keys are REMOTE tool names — the name on the server, not the namespaced
// registry name (mcp_<server>_<tool>) — because a frequency ranking exists to
// pick which remote tools to expose; the namespacing is applied later by
// buildSpecs.
//
// Persistence is best-effort by contract: usage tracking must never break a
// tool call, so every disk error is logged as a warning and swallowed. A
// store built with an empty path (or a nil *usageStore) keeps its counters in
// memory only.
type usageStore struct {
	mu     sync.Mutex
	path   string
	counts map[string]map[string]uint64
}

// usageFileIndent is the indentation of the counter file. The file is a
// nested object
//
//	{"<server>": {"<remoteTool>": <call count>}}
//
// kept indented so it stays readable and greppable when a user inspects
// <LeleDir>/mcp_usage.json by hand.
const usageFileIndent = "  "

// loadUsageStore builds a usage store persisted at path. An empty path yields
// a memory-only store that never touches the disk.
//
// Loading is deliberately lenient: a missing file is the normal first-run case
// (no log), and an unreadable or corrupt file starts an empty store with a
// single warning — a damaged counter file must never keep lele from starting
// or from calling tools. Construction never fails.
func loadUsageStore(path string) *usageStore {
	s := &usageStore{path: path, counts: make(map[string]map[string]uint64)}
	if path == "" {
		return s
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			warnUsage("could not read the MCP usage file, starting with empty counts", path, err)
		}
		return s
	}
	var loaded map[string]map[string]uint64
	if err := json.Unmarshal(data, &loaded); err != nil {
		warnUsage("could not parse the MCP usage file, starting with empty counts", path, err)
		return s
	}
	for server, tools := range loaded {
		// A "null" server value (or an empty object) carries no counts: skip it
		// so countsFor never hands back a nil map.
		if len(tools) == 0 {
			continue
		}
		s.counts[server] = tools
	}
	return s
}

// warnUsage logs one non-fatal usage-store problem. The message carries the
// file path and the error string only — never a tool argument.
func warnUsage(message, path string, err error) {
	logger.WarnCF("mcp", message, map[string]interface{}{
		"path":  path,
		"error": err.Error(),
	})
}

// inc records one call of the remote tool remote on server and, when the store
// is file-backed, writes the whole counter file back synchronously.
//
// The write happens under mu on purpose: the file is a few hundred bytes and
// remote calls are infrequent, so serialising here is cheaper than the
// bookkeeping a background writer would need — and it guarantees a process
// that exits right after a call still leaves the count on disk. Never returns
// an error: persistence failures are warnings, see the type comment.
//
// A nil receiver is a no-op, mirroring sessionToolSet, so a Manager built
// without a store still calls tools normally.
func (s *usageStore) inc(server, remote string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.counts == nil {
		s.counts = make(map[string]map[string]uint64)
	}
	tools := s.counts[server]
	if tools == nil {
		tools = make(map[string]uint64)
		s.counts[server] = tools
	}
	tools[remote]++

	if s.path == "" {
		return
	}
	if err := s.saveLocked(); err != nil {
		warnUsage("could not persist the MCP usage file", s.path, err)
	}
}

// saveLocked writes the whole counter map to s.path atomically: the JSON goes
// to a temp file in the SAME directory (so the rename cannot cross a
// filesystem), is chmodded 0600 (the file stays user-private like mcp_auth),
// closed and then renamed over the target. A reader therefore always sees
// either the previous complete file or the new complete file, never a partial
// one. The caller must hold s.mu.
//
// No fsync is issued: a lost increment after a hard crash is harmless (the
// counts only rank tools), and calling sync on every remote tool call would be
// a real cost. Temp files are removed on every failure path.
func (s *usageStore) saveLocked() error {
	data, err := json.MarshalIndent(s.counts, "", usageFileIndent)
	if err != nil {
		return fmt.Errorf("marshal usage: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".mcp-usage-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	// CreateTemp already yields 0600 on Unix; be explicit for portability and
	// for umask-independence. Must run before Close (fchmod on a closed fd
	// fails on Linux).
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace %s: %w", s.path, err)
	}
	committed = true
	return nil
}

// countsFor returns a COPY of one server's call counts, keyed by remote tool
// name, so a caller can sort or mutate the result without racing a concurrent
// inc. The result is a non-nil (possibly empty) map when the store exists and
// nil for a nil receiver, which lets callers range over it unconditionally.
func (s *usageStore) countsFor(server string) map[string]uint64 {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]uint64, len(s.counts[server]))
	for remote, count := range s.counts[server] {
		out[remote] = count
	}
	return out
}
