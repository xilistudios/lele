package agent

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/store"
)

type sessionKVSnapshot struct {
	aliases map[string]string
	agents  map[string]string
	seqs    map[string]uint64
	others  []string
}

func captureSessionKV(t *testing.T, st *store.Store) sessionKVSnapshot {
	t.Helper()
	snap := sessionKVSnapshot{
		aliases: map[string]string{},
		agents:  map[string]string{},
		seqs:    map[string]uint64{},
	}
	keys, err := st.KV().Keys("")
	if err != nil {
		t.Fatalf("list KV keys: %v", err)
	}
	for _, key := range keys {
		value, found, err := st.KV().Get(key)
		if err != nil || !found {
			t.Fatalf("KV get %q: found=%t err=%v", key, found, err)
		}
		switch {
		case strings.HasPrefix(key, sessAliasKeyPrefix):
			snap.aliases[strings.TrimPrefix(key, sessAliasKeyPrefix)] = value
		case strings.HasPrefix(key, sessAgentKeyPrefix):
			snap.agents[strings.TrimPrefix(key, sessAgentKeyPrefix)] = value
		case strings.HasPrefix(key, sessSeqKeyPrefix):
			var n uint64
			if _, err := fmt.Sscanf(value, "%d", &n); err != nil {
				t.Fatalf("seq key %q holds non-numeric value %q: %v", key, value, err)
			}
			snap.seqs[strings.TrimPrefix(key, sessSeqKeyPrefix)] = n
		default:
			snap.others = append(snap.others, key)
		}
	}
	return snap
}

func (s sessionKVSnapshot) count() int {
	return len(s.aliases) + len(s.agents) + len(s.seqs) + len(s.others)
}

func openSessionStateStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "session-state.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newSessionStateLoop(t *testing.T) (*AgentLoop, *store.Store) {
	t.Helper()
	st := openSessionStateStore(t)
	return newSessionStateLoopOn(st), st
}

func newSessionStateLoopOn(st *store.Store) *AgentLoop {
	return &AgentLoop{dbStore: st, registry: &AgentRegistry{}}
}

func silenceSessionStateLogs(t *testing.T) {
	t.Helper()
	prev := logger.GetLevel()
	logger.SetLevel(logger.FATAL)
	t.Cleanup(func() { logger.SetLevel(prev) })
}

func rawAlias(al *AgentLoop, baseKey string) (string, bool) {
	v, ok := al.sessionAliases.Load(baseKey)
	return aliasString(v), ok
}

func rawAgent(al *AgentLoop, sessionKey string) (string, bool) {
	v, ok := al.sessionAgents.Load(sessionKey)
	return aliasString(v), ok
}

func rawSubagentAgent(al *AgentLoop, sessionKey string) (string, bool) {
	v, ok := al.subagentSessionAgent.Load(sessionKey)
	return aliasString(v), ok
}

func aliasString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func rawSeed(al *AgentLoop, baseKey string) (uint64, bool) {
	al.seqSeedMu.Lock()
	defer al.seqSeedMu.Unlock()
	n, ok := al.seqSeeds[baseKey]
	return n, ok
}

func rawPersisted(al *AgentLoop, baseKey string) (uint64, bool) {
	al.seqSeedMu.Lock()
	defer al.seqSeedMu.Unlock()
	n, ok := al.seqSeedPersisted[baseKey]
	return n, ok
}

func seedSnapshot(al *AgentLoop) map[string]uint64 {
	al.seqSeedMu.Lock()
	defer al.seqSeedMu.Unlock()
	out := make(map[string]uint64, len(al.seqSeeds))
	for k, v := range al.seqSeeds {
		out[k] = v
	}
	return out
}

func persistedSnapshot(al *AgentLoop) map[string]uint64 {
	al.seqSeedMu.Lock()
	defer al.seqSeedMu.Unlock()
	out := make(map[string]uint64, len(al.seqSeedPersisted))
	for k, v := range al.seqSeedPersisted {
		out[k] = v
	}
	return out
}

func TestSessionStateKeyNamespace(t *testing.T) {
	prefixes := map[string]string{
		"alias": sessAliasKeyPrefix,
		"agent": sessAgentKeyPrefix,
		"seq":   sessSeqKeyPrefix,
	}
	seen := map[string]string{}
	for name, prefix := range prefixes {
		if !strings.HasPrefix(prefix, "sess:") {
			t.Errorf("prefix %s (%q) is not namespaced under \"sess:\"", name, prefix)
		}
		if !strings.HasSuffix(prefix, ":") {
			t.Errorf("prefix %s (%q) must end in ':' so two keys cannot run together", name, prefix)
		}
		if other, dup := seen[prefix]; dup {
			t.Errorf("prefix %s and prefix %s collide on %q", name, other, prefix)
		}
		seen[prefix] = name
	}

	for a, pa := range prefixes {
		for b, pb := range prefixes {
			if a != b && strings.HasPrefix(pb, pa) {
				t.Errorf("prefix %s (%q) is a prefix of %s (%q)", a, pa, b, pb)
			}
		}
	}
}

func TestSessionStateWritesStayInTheirNamespace(t *testing.T) {
	al, st := newSessionStateLoop(t)

	al.setSessionAlias("base", "base:chat:1")
	al.setSessionAgent("base:chat:1", "main")
	al.setSubagentSessionAgent("subagent:1", "coder")
	al.nextConversationSessionKey("base")

	snap := captureSessionKV(t, st)
	if len(snap.others) != 0 {
		t.Errorf("session state wrote keys outside its namespaces: %v", snap.others)
	}
	if got := snap.aliases["base"]; got != "base:chat:1" {
		t.Errorf("sess:alias:base = %q, want %q", got, "base:chat:1")
	}
	if got := snap.agents["base:chat:1"]; got != "main" {
		t.Errorf("user agent row = %q, want %q", got, "main")
	}
	if got := snap.agents["subagent:1"]; got != "coder" {
		t.Errorf("subagent agent row = %q, want %q", got, "coder")
	}
	if got := snap.seqs["base"]; got != 1 {
		t.Errorf("sess:seq:base = %d, want 1", got)
	}
}

func TestSessionStateKV(t *testing.T) {
	t.Run("no store returns nil", func(t *testing.T) {
		al := &AgentLoop{}
		if kv := al.sessionStateKV(); kv != nil {
			t.Errorf("sessionStateKV() = %p, want nil", kv)
		}
	})

	t.Run("store returns its KV repo", func(t *testing.T) {
		al, st := newSessionStateLoop(t)
		if kv := al.sessionStateKV(); kv == nil {
			t.Fatal("sessionStateKV() = nil, want the store's KV repo")
		} else if kv != st.KV() {
			t.Error("sessionStateKV() returned a repo other than the store's")
		}
		if a, b := al.sessionStateKV(), al.sessionStateKV(); a != b {
			t.Error("sessionStateKV() is not stable across calls")
		}
	})
}

func TestSetSessionAlias(t *testing.T) {
	t.Run("mirrors memory and KV", func(t *testing.T) {
		al, st := newSessionStateLoop(t)

		al.setSessionAlias("web:chat:1", "web:chat:2")

		if got := al.ResolveSessionKey("web:chat:1"); got != "web:chat:2" {
			t.Errorf("ResolveSessionKey = %q, want %q", got, "web:chat:2")
		}
		if got, ok := rawAlias(al, "web:chat:1"); !ok || got != "web:chat:2" {
			t.Errorf("in-memory alias = %q/%t, want %q/true", got, ok, "web:chat:2")
		}
		if got := captureSessionKV(t, st).aliases["web:chat:1"]; got != "web:chat:2" {
			t.Errorf("durable alias = %q, want %q", got, "web:chat:2")
		}
	})

	t.Run("empty keys are dropped", func(t *testing.T) {
		cases := []struct{ name, base, active string }{
			{"empty base", "", "active"},
			{"empty active", "base", ""},
			{"both empty", "", ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				al, st := newSessionStateLoop(t)
				al.setSessionAlias(tc.base, tc.active)

				if n := captureSessionKV(t, st).count(); n != 0 {
					t.Errorf("wrote %d KV rows for (%q,%q), want 0", n, tc.base, tc.active)
				}
				if _, ok := rawAlias(al, tc.base); ok {
					t.Errorf("in-memory alias stored for (%q,%q), want dropped", tc.base, tc.active)
				}
			})
		}
	})

	t.Run("overwrite replaces in place", func(t *testing.T) {
		al, st := newSessionStateLoop(t)

		for _, active := range []string{"a:chat:1", "a:chat:2", "a:chat:3"} {
			al.setSessionAlias("base", active)
		}

		snap := captureSessionKV(t, st)
		if got := snap.aliases["base"]; got != "a:chat:3" {
			t.Errorf("durable alias = %q, want %q", got, "a:chat:3")
		}
		if len(snap.aliases) != 1 {
			t.Errorf("%d alias rows after 3 writes, want 1: %v", len(snap.aliases), snap.aliases)
		}
	})

	t.Run("keys that contain LIKE wildcards round-trip", func(t *testing.T) {
		al, st := newSessionStateLoop(t)

		bases := []string{"ch%at", "ch_at", "ch\\at", "pct:100%:chat"}
		for i, base := range bases {
			al.setSessionAlias(base, fmt.Sprintf("%s:chat:%d", base, i+1))
		}

		reloaded := newSessionStateLoopOn(st)
		reloaded.loadDurableSessionState()
		snap := captureSessionKV(t, st)
		for i, base := range bases {
			want := fmt.Sprintf("%s:chat:%d", base, i+1)
			if got := snap.aliases[base]; got != want {
				t.Errorf("durable alias for %q = %q, want %q", base, got, want)
			}
			if got, ok := rawAlias(reloaded, base); !ok || got != want {
				t.Errorf("reloaded alias for %q = %q/%t, want %q/true", base, got, ok, want)
			}
		}
	})

	t.Run("alias write touches nothing else", func(t *testing.T) {
		al, st := newSessionStateLoop(t)
		al.setSessionAlias("base", "base:chat:9")

		snap := captureSessionKV(t, st)
		if len(snap.agents) != 0 || len(snap.seqs) != 0 {
			t.Errorf("alias write leaked into other namespaces: agents=%v seqs=%v", snap.agents, snap.seqs)
		}
		if n := al.sessionKeySeq.Load(); n != 0 {
			t.Errorf("alias write advanced the global sequence to %d, want 0", n)
		}
		if len(seedSnapshot(al)) != 0 {
			t.Errorf("alias write created sequence seeds: %v", seedSnapshot(al))
		}
	})
}

func TestNextConversationSessionKey(t *testing.T) {
	t.Run("empty base returns empty", func(t *testing.T) {
		al, st := newSessionStateLoop(t)

		if got := al.nextConversationSessionKey(""); got != "" {
			t.Errorf("nextConversationSessionKey(\"\") = %q, want \"\"", got)
		}
		if n := al.sessionKeySeq.Load(); n != 0 {
			t.Errorf("global sequence advanced to %d on an empty base, want 0", n)
		}
		if n := captureSessionKV(t, st).count(); n != 0 {
			t.Errorf("empty base wrote %d KV rows, want 0", n)
		}
	})

	t.Run("increments per base", func(t *testing.T) {
		al, st := newSessionStateLoop(t)

		for n := 1; n <= 3; n++ {
			want := fmt.Sprintf("tg:1:chat:%d", n)
			if got := al.nextConversationSessionKey("tg:1"); got != want {
				t.Fatalf("call %d = %q, want %q", n, got, want)
			}
		}
		if got := captureSessionKV(t, st).seqs["tg:1"]; got != 3 {
			t.Errorf("durable seed = %d, want 3", got)
		}
	})

	t.Run("counter is global across bases", func(t *testing.T) {
		al, _ := newSessionStateLoop(t)

		first := al.nextConversationSessionKey("a")
		second := al.nextConversationSessionKey("b")

		if first != "a:chat:1" {
			t.Errorf("a's first key = %q, want %q", first, "a:chat:1")
		}
		if second != "b:chat:2" {
			t.Errorf("b's first key = %q, want %q", second, "b:chat:2")
		}
	})

	t.Run("the value handed out is the value persisted", func(t *testing.T) {
		al, st := newSessionStateLoop(t)

		for i := 1; i <= 5; i++ {
			got := al.nextConversationSessionKey("base")
			want := fmt.Sprintf("base:chat:%d", i)
			if got != want {
				t.Fatalf("call %d = %q, want %q", i, got, want)
			}
			if seed := captureSessionKV(t, st).seqs["base"]; seed != uint64(i) {
				t.Fatalf("after %q, durable seed = %d, want %d", got, seed, i)
			}
		}
	})

	t.Run("one row per base no matter how many keys", func(t *testing.T) {
		al, st := newSessionStateLoop(t)

		for i := 1; i <= 50; i++ {
			if got, want := al.nextConversationSessionKey("base"), fmt.Sprintf("base:chat:%d", i); got != want {
				t.Fatalf("call %d = %q, want %q", i, got, want)
			}
		}
		snap := captureSessionKV(t, st)
		if len(snap.seqs) != 1 {
			t.Errorf("%d seed rows for one base, want 1: %v", len(snap.seqs), snap.seqs)
		}
		if snap.seqs["base"] != 50 {
			t.Errorf("durable seed = %d, want 50", snap.seqs["base"])
		}
		if n := snap.count(); n != 1 {
			t.Errorf("%d KV rows in total, want 1", n)
		}
	})

	t.Run("no KV write when the number does not move", func(t *testing.T) {
		al, st := newSessionStateLoop(t)

		al.seqSeedMu.Lock()
		al.seqSeeds = map[string]uint64{"base": 40}
		al.seqSeedPersisted = map[string]uint64{"base": 40}
		al.seqSeedMu.Unlock()

		if err := st.KV().Delete(sessSeqKeyPrefix + "base"); err != nil {
			t.Fatalf("delete seed row: %v", err)
		}

		if got := al.nextConversationSessionKey("base"); got != "base:chat:40" {
			t.Errorf("key = %q, want %q (the seed must win over the fresh counter)", got, "base:chat:40")
		}
		if got := captureSessionKV(t, st).seqs["base"]; got != 0 {
			t.Errorf("the reloaded number was re-persisted as %d, want no write at all", got)
		}

		if got := al.sessionKeySeq.Load(); got < 40 {
			t.Errorf("global counter = %d, want at least the seeded 40", got)
		}
		if got := al.nextConversationSessionKey("other"); got != "other:chat:41" {
			t.Errorf("unrelated base got %q, want %q", got, "other:chat:41")
		}
		if got := captureSessionKV(t, st).seqs["other"]; got != 41 {
			t.Errorf("unrelated base seed = %d, want 41 (a moved number must persist)", got)
		}
	})

	t.Run("base key containing colons", func(t *testing.T) {
		al, st := newSessionStateLoop(t)

		base := "agent:main:tg:123:direct:456"
		if got := al.nextConversationSessionKey(base); got != base+":chat:1" {
			t.Errorf("key = %q, want %q", got, base+":chat:1")
		}
		snap := captureSessionKV(t, st)
		if snap.seqs[base] != 1 {
			t.Errorf("seed stored under the wrong base: %v", snap.seqs)
		}
	})
}

func TestNextChatSessionConcurrent(t *testing.T) {
	al, st := newSessionStateLoop(t)

	const goroutines = 32
	const perGoroutine = 8

	var wg sync.WaitGroup
	keys := make([]string, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				keys[g*perGoroutine+i] = al.nextConversationSessionKey("base")
			}
		}(g)
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			t.Fatalf("duplicate conversation key %q handed out", key)
		}
		seen[key] = true
		if !strings.HasPrefix(key, "base:chat:") {
			t.Errorf("malformed key %q", key)
		}
	}

	seed := captureSessionKV(t, st).seqs["base"]
	if seed != goroutines*perGoroutine {
		t.Errorf("durable seed = %d, want %d", seed, goroutines*perGoroutine)
	}
	if got := al.sessionKeySeq.Load(); got < seed {
		t.Errorf("global counter %d is below the durable seed %d", got, seed)
	}

	want := fmt.Sprintf("base:chat:%d", goroutines*perGoroutine+1)
	if got := al.nextConversationSessionKey("base"); got != want {
		t.Errorf("key after contention = %q, want %q", got, want)
	}
}

func TestBumpSessionKeySeq(t *testing.T) {
	al := &AgentLoop{}

	cases := []struct {
		name string
		min  uint64
		want uint64
	}{
		{"raises from zero", 5, 5},
		{"raises further", 9, 9},
		{"never lowers", 3, 9},
		{"zero is a no-op", 0, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al.bumpSessionKeySeq(tc.min)
			if got := al.sessionKeySeq.Load(); got != tc.want {
				t.Errorf("after bump(%d): counter = %d, want %d", tc.min, got, tc.want)
			}
		})
	}
}

func TestSessionStateSurvivesRestart(t *testing.T) {
	base := "agent:main:tg:123:direct:456"
	subagentKey := "agent:coder:subagent:task-1"

	st := openSessionStateStore(t)

	first := newSessionStateLoopOn(st)
	first.setSessionAlias(base, first.nextConversationSessionKey(base))
	first.setSessionAgent(base+":chat:1", "coder")
	first.setSubagentSessionAgent(subagentKey, "fixer")

	wantAlias := first.ResolveSessionKey(base)
	if wantAlias != base+":chat:1" {
		t.Fatalf("process 1 alias = %q, want %q", wantAlias, base+":chat:1")
	}

	second := newSessionStateLoopOn(st)
	aliases, agents := second.loadDurableSessionState()
	if aliases != 1 {
		t.Errorf("aliases loaded = %d, want 1", aliases)
	}
	if agents != 2 {
		t.Errorf("agents loaded = %d, want 2 (user + subagent)", agents)
	}

	if got := second.ResolveSessionKey(base); got != wantAlias {
		t.Errorf("alias after restart = %q, want %q", got, wantAlias)
	}
	if got := second.getSessionAgent(base); got != "coder" {
		t.Errorf("session agent after restart = %q, want %q", got, "coder")
	}
	if got := second.getSessionAgent(subagentKey); got != "fixer" {
		t.Errorf("subagent agent after restart = %q, want %q", got, "fixer")
	}
	if got := second.nextConversationSessionKey(base); got != base+":chat:2" {
		t.Errorf("first conversation key after restart = %q, want %q", got, base+":chat:2")
	}
	second.setSessionAlias(base, base+":chat:2")
	second.setSessionAgent(base+":chat:2", "coder")

	third := newSessionStateLoopOn(st)
	third.loadDurableSessionState()
	if got := third.ResolveSessionKey(base); got != base+":chat:2" {
		t.Errorf("alias after second restart = %q, want %q", got, base+":chat:2")
	}
	if got := third.getSessionAgent(base); got != "coder" {
		t.Errorf("session agent after second restart = %q, want %q", got, "coder")
	}
	if got := third.nextConversationSessionKey(base); got != base+":chat:3" {
		t.Errorf("conversation key after second restart = %q, want %q", got, base+":chat:3")
	}
}

func TestSessionStateRestartKeepsIndependentBases(t *testing.T) {
	st := openSessionStateStore(t)

	first := newSessionStateLoopOn(st)
	keys := []string{
		first.nextConversationSessionKey("a"),
		first.nextConversationSessionKey("b"),
		first.nextConversationSessionKey("a"),
		first.nextConversationSessionKey("c"),
	}
	if keys[0] != "a:chat:1" || keys[1] != "b:chat:2" || keys[2] != "a:chat:3" || keys[3] != "c:chat:4" {
		t.Fatalf("pre-restart keys = %v", keys)
	}
	first.deleteDurableSessionAlias("b")

	second := newSessionStateLoopOn(st)
	second.loadDurableSessionState()

	if got := second.nextConversationSessionKey("a"); got != "a:chat:5" {
		t.Errorf("a after restart = %q, want %q (must exceed a:chat:3)", got, "a:chat:5")
	}
	if got := second.nextConversationSessionKey("c"); got != "c:chat:6" {
		t.Errorf("c after restart = %q, want %q", got, "c:chat:6")
	}
	bKey := second.nextConversationSessionKey("b")
	if bKey == "b:chat:1" || bKey == "b:chat:2" {
		t.Errorf("b after its reset got %q, want a number no base has used", bKey)
	}
	if !strings.HasPrefix(bKey, "b:chat:") {
		t.Errorf("b got a malformed key %q", bKey)
	}

	seen := map[string]bool{}
	for _, k := range append(keys, second.nextConversationSessionKey("a"), bKey) {
		if seen[k] {
			t.Errorf("duplicate conversation key across restarts: %q", k)
		}
		seen[k] = true
	}
}

func TestSessionStateDegradesWithoutStore(t *testing.T) {
	silenceSessionStateLogs(t)
	al := &AgentLoop{registry: &AgentRegistry{}}

	if kv := al.sessionStateKV(); kv != nil {
		t.Fatalf("sessionStateKV() = %p, want nil for the no-store case", kv)
	}

	al.setSessionAlias("base", "base:chat:1")
	al.setSessionAgent("base:chat:1", "coder")
	al.setSubagentSessionAgent("agent:main:subagent:1", "fixer")

	if got := al.ResolveSessionKey("base"); got != "base:chat:1" {
		t.Errorf("alias = %q, want %q", got, "base:chat:1")
	}
	if got := al.getSessionAgent("base"); got != "coder" {
		t.Errorf("agent via alias = %q, want %q", got, "coder")
	}
	if got := al.getSessionAgent("agent:main:subagent:1"); got != "fixer" {
		t.Errorf("subagent agent = %q, want %q", got, "fixer")
	}
	if got := al.nextConversationSessionKey("base"); got != "base:chat:1" {
		t.Errorf("conversation key = %q, want %q", got, "base:chat:1")
	}
	if got := al.nextConversationSessionKey("base"); got != "base:chat:2" {
		t.Errorf("second conversation key = %q, want %q", got, "base:chat:2")
	}

	aliases, agents := al.loadDurableSessionState()
	if aliases != 0 || agents != 0 {
		t.Errorf("load without a store = (%d,%d), want (0,0)", aliases, agents)
	}
	al.deleteDurableSessionAlias("base")
	al.deleteDurableSessionAgent("base:chat:1")
	if got := al.ResolveSessionKey("base"); got != "base" {
		t.Errorf("alias after delete = %q, want %q", got, "base")
	}
}

func TestSessionStateSurvivesKVFailure(t *testing.T) {
	silenceSessionStateLogs(t)

	st := openSessionStateStore(t)
	al := newSessionStateLoopOn(st)

	al.setSessionAgent("warm", "main")

	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	al.setSessionAlias("base", "base:chat:1")
	al.setSessionAgent("base:chat:1", "coder")
	al.setSubagentSessionAgent("agent:main:subagent:1", "fixer")
	if got := al.nextConversationSessionKey("base"); got != "base:chat:1" {
		t.Errorf("conversation key = %q, want %q", got, "base:chat:1")
	}
	al.deleteDurableSessionAlias("base")
	al.deleteDurableSessionAgent("base:chat:1")
	aliases, agents := al.loadDurableSessionState()

	if aliases != 0 || agents != 0 {
		t.Errorf("load against a dead store = (%d,%d), want (0,0)", aliases, agents)
	}
	if _, ok := rawAgent(al, "base:chat:1"); ok {
		t.Error("delete did not clear the in-memory agent map")
	}
	al.setSessionAgent("base:chat:2", "researcher")
	if got, ok := rawAgent(al, "base:chat:2"); !ok || got != "researcher" {
		t.Errorf("in-memory agent after KV failure = %q/%t, want %q/true", got, ok, "researcher")
	}
	if got, ok := rawSubagentAgent(al, "agent:main:subagent:1"); !ok || got != "fixer" {
		t.Errorf("in-memory subagent agent = %q/%t, want %q/true", got, ok, "fixer")
	}
	if got := al.nextConversationSessionKey("base"); got != "base:chat:2" {
		t.Errorf("conversation key after KV failure = %q, want %q", got, "base:chat:2")
	}
	if got, ok := rawAgent(al, "warm"); !ok || got != "main" {
		t.Errorf("pre-existing in-memory state was lost: %q/%t", got, ok)
	}
}

func equalUint64Maps(got, want map[string]uint64) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
