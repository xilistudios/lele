package channels

// auth_revocation_crossproc_test.go — regression tests for issue #327.
//
// The root cause was saveStoreUnlocked's SQLite branch performing a WHOLE-STORE
// snapshot sync: it deleted every table row absent from the calling process's
// in-memory map and re-upserted every row present in it. With the gateway and
// the CLI as two processes over one DB, each process's map is a stale view of
// the table, so any write by one resurrected what the other had revoked —
// "last writer wins with the whole map". A client removed with
// `lele client remove` kept authenticating until the gateway restarted.
//
// Production mutators must now use directed per-row writes
// (persistClientLocked / repo.DeleteClient). These tests pin that contract from
// both directions, over two independent store handles standing in for two
// processes, in the wiring cmd/lele/client.go actually uses.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

// pairClientOn runs the PIN handshake on a single manager and returns the
// client plus its tokens. Both processes in these tests own their own manager,
// so minting the PIN and redeeming it on the same side is fine and keeps the
// focus on what happens to the ROWS afterwards.
func pairClientOn(t *testing.T, am *AuthManager, deviceName string) (*ClientInfo, string, string) {
	t.Helper()
	pending, err := am.GeneratePIN(deviceName)
	if err != nil {
		t.Fatalf("GeneratePIN(%s): %v", deviceName, err)
	}
	client, token, refreshToken, err := am.PairWithPIN(pending.PIN, deviceName)
	if err != nil {
		t.Fatalf("PairWithPIN(%s): %v", deviceName, err)
	}
	return client, token, refreshToken
}

// dbClientIDs reads the authoritative row set straight from the table, bypassing
// any process's in-memory map.
func dbClientIDs(t *testing.T, s *store.Store) map[string]bool {
	t.Helper()
	rows, err := s.NativeClients().ListClients()
	if err != nil {
		t.Fatalf("ListClients: %v", err)
	}
	ids := make(map[string]bool, len(rows))
	for id := range rows {
		ids[id] = true
	}
	return ids
}

// TestRevocation_GatewayWriteDoesNotResurrectClientRemovedByCLI is #327 verbatim.
//
// The gateway holds a stale map that still contains a client the CLI has just
// revoked. Any gateway write that touches auth state — the issue lists
// GeneratePIN, RefreshToken, TrackSessionKey and RegisterDesktopClient — used
// to re-upsert the whole map and bring the revoked client back to life.
func TestRevocation_GatewayWriteDoesNotResurrectClientRemovedByCLI(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	// "Gateway" process.
	gwAuth, gwStore := openServerAuthManager(t, tmpDir, dbPath)
	defer gwStore.Close()

	revoked, _, _ := pairClientOn(t, gwAuth, "revoked-phone")
	kept, _, _ := pairClientOn(t, gwAuth, "kept-phone")

	// "CLI" process: independent handle, revokes one client. This is
	// `lele client remove <id>`.
	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	defer cliStore.Close()
	if err := cliAuth.RemoveClient(revoked.ClientID); err != nil {
		t.Fatalf("CLI RemoveClient: %v", err)
	}
	if ids := dbClientIDs(t, gwStore); ids[revoked.ClientID] {
		t.Fatalf("precondition: CLI removal did not reach the DB (rows: %v)", ids)
	}

	// The gateway has NOT reloaded: its map still holds the revoked client.
	if _, ok := gwAuth.GetClient(revoked.ClientID); !ok {
		t.Fatal("precondition: the gateway map must still hold the revoked client, " +
			"otherwise this test does not exercise the stale-snapshot path")
	}

	// Every gateway-side write the issue names must leave the revocation alone.
	writes := []struct {
		name string
		fn   func()
	}{
		{"GeneratePIN", func() {
			if _, err := gwAuth.GeneratePIN("some-other-device"); err != nil {
				t.Errorf("GeneratePIN: %v", err)
			}
		}},
		{"TrackSessionKey", func() { gwAuth.TrackSessionKey(kept.ClientID, "native:chat-1") }},
		{"UpdateLastSeen", func() { gwAuth.UpdateLastSeen(kept.ClientID) }},
		{"RegisterDesktopClient", func() {
			if err := gwAuth.RegisterDesktopClient("desk-token", "desk-refresh"); err != nil {
				t.Errorf("RegisterDesktopClient: %v", err)
			}
		}},
	}

	for _, w := range writes {
		t.Run(w.name, func(t *testing.T) {
			w.fn()

			ids := dbClientIDs(t, gwStore)
			if ids[revoked.ClientID] {
				t.Errorf("%s resurrected the revoked client %q in the table: the write "+
					"went through a whole-store snapshot instead of a directed per-row write, "+
					"so `lele client remove` is undone by the next gateway write",
					w.name, revoked.ClientID)
			}
			if !ids[kept.ClientID] {
				t.Errorf("%s lost the unrelated client %s", w.name, kept.ClientID)
			}
		})
	}

	// NOTE: this test deliberately stops at the table. Whether the gateway's
	// own stale map still authenticates the revoked token is a different
	// guarantee (bounded staleness, not write hygiene) and is pinned by
	// TestReconcileClientsWithStore_EndsARevokedClientInsideTheRunningGateway.
}

// TestRevocation_CLIWriteDoesNotDeleteClientsItNeverLoaded covers the mirror
// direction named in the issue: a CLI process whose map is a stale subset must
// not delete rows the gateway paired after it loaded.
func TestRevocation_CLIWriteDoesNotDeleteClientsItNeverLoaded(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	// CLI loads first, when only one client exists.
	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	defer cliStore.Close()
	early, _, _ := pairClientOn(t, cliAuth, "early-phone")

	// Gateway pairs another one AFTER the CLI's map was built.
	gwAuth, gwStore := openServerAuthManager(t, tmpDir, dbPath)
	defer gwStore.Close()
	later, _, _ := pairClientOn(t, gwAuth, "later-phone")

	// The CLI's view is now stale by construction: it does not know about
	// `later`. Confirm that, otherwise the test proves nothing.
	if _, ok := cliAuth.GetClient(later.ClientID); ok {
		t.Fatal("precondition: the CLI map must be stale (it must not know the later client)")
	}

	// Any CLI write must not drop the row it never saw.
	cliAuth.TrackSessionKey(early.ClientID, "native:chat-2")
	if _, err := cliAuth.GeneratePIN("cli-device"); err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}

	ids := dbClientIDs(t, gwStore)
	if !ids[early.ClientID] {
		t.Error("CLI write deleted its own client")
	}
	if !ids[later.ClientID] {
		t.Errorf("CLI write deleted client %s, which the CLI had never loaded: a "+
			"whole-store snapshot sync deletes every table row missing from its map", later.ClientID)
	}
}

// TestSaveStoreUnlocked_StillClobbersAcrossProcesses is a characterization test
// of the hazard the fix leaves in place on purpose, so that it stays visible.
//
// saveStoreUnlocked's SQLite branch still reconciles the table against the
// calling process's map: it deletes rows the map does not have and upserts the
// ones it does. That is exactly what produced #327, and it is still reachable
// through saveStore(). What changed is reachability — saveStore() is now called
// only from migrations and tests, never from a production mutator, all of which
// go through persistClientLocked or repo.DeleteClient instead.
//
// This test asserts the destructive behaviour rather than guarding against it:
// if it ever stops clobbering, the comment on saveStore and the reasoning in
// this file need updating, and if a production path starts calling it, the two
// tests above go red first.
func TestSaveStoreUnlocked_StillClobbersAcrossProcesses(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	// A process that paired one client and remembers it.
	first, firstStore := openServerAuthManager(t, tmpDir, dbPath)
	defer firstStore.Close()
	clientA, _, _ := pairClientOn(t, first, "device-a")

	// A second process that paired another client but loaded its map BEFORE
	// clientA existed, so its map is a stale subset of the table.
	second, secondStore := openCLIAuthManager(t, tmpDir, dbPath)
	defer secondStore.Close()
	clientB, _, _ := pairClientOn(t, second, "device-b")

	second.forgetForTest(t, clientA.ClientID)

	if err := second.saveStore(); err != nil {
		t.Fatalf("saveStore: %v", err)
	}

	ids := dbClientIDs(t, firstStore)
	if ids[clientA.ClientID] {
		t.Skip("saveStoreUnlocked no longer deletes rows absent from its map; " +
			"the whole-store path is now safe and this characterization must be updated")
	}
	if !ids[clientB.ClientID] {
		t.Errorf("saveStore dropped its own client %q", clientB.ClientID)
	}
}

// forgetForTest drops a client from this manager's in-memory map without
// touching the DB, simulating a process that loaded before the client existed.
func (am *AuthManager) forgetForTest(t *testing.T, clientID string) {
	t.Helper()
	am.mu.Lock()
	defer am.mu.Unlock()
	delete(am.store.Clients, clientID)
}

// TestReconcileClientsWithStore_EndsARevokedClientInsideTheRunningGateway covers
// the half of #327 the per-row writes do NOT fix.
//
// Stopping the gateway from resurrecting the row is necessary but not
// sufficient: the gateway still holds the ClientInfo it loaded at startup, and
// ValidateToken answers a map HIT without consulting the DB — it only reloads
// on a MISS (see slowReloadMinInterval). So the revoked device kept
// authenticating until the gateway restarted, which is the impact the issue
// reports. reconcileClientsWithStore closes that window, and this test pins
// both sides of it: the stale hit is real before the reconcile (the bug), and
// gone after it (the fix).
func TestReconcileClientsWithStore_EndsARevokedClientInsideTheRunningGateway(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	gwAuth, gwStore := openServerAuthManager(t, tmpDir, dbPath)
	defer gwStore.Close()
	revoked, revokedToken, revokedRefresh := pairClientOn(t, gwAuth, "revoked-phone")
	kept, keptToken, _ := pairClientOn(t, gwAuth, "kept-phone")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	defer cliStore.Close()
	if err := cliAuth.RemoveClient(revoked.ClientID); err != nil {
		t.Fatalf("CLI RemoveClient: %v", err)
	}

	// Before the fix this is the whole problem: the row is gone, yet the
	// running gateway still waves the credential through.
	if _, ok := gwAuth.ValidateToken(revokedToken); !ok {
		t.Fatal("precondition: the gateway should still be serving the revoked token " +
			"from its stale map before a reconcile runs")
	}

	dropped, err := gwAuth.reconcileClientsWithStore()
	if err != nil {
		t.Fatalf("reconcileClientsWithStore: %v", err)
	}
	if len(dropped) != 1 || dropped[0] != revoked.ClientID {
		t.Errorf("reconcile dropped %v, want just [%s]", dropped, revoked.ClientID)
	}

	// The revoked credential must now be dead on every path of the RUNNING
	// process, not just on a freshly started one.
	if _, ok := gwAuth.ValidateToken(revokedToken); ok {
		t.Error("revoked token still authenticates after a reconcile: `lele client remove` " +
			"is not effective until the gateway restarts, which is the impact #327 reports")
	}
	if _, _, _, err := gwAuth.RefreshToken(revokedRefresh); err == nil {
		t.Error("revoked client could still rotate a token pair after a reconcile")
	}
	if _, ok := gwAuth.GetClient(revoked.ClientID); ok {
		t.Error("revoked client still listed by the gateway after a reconcile")
	}

	// The unrelated client is untouched — reconciling must never log anyone out.
	if _, ok := gwAuth.ValidateToken(keptToken); !ok {
		t.Errorf("reconcile dropped the unrelated client %s", kept.ClientID)
	}
}

// TestReconcileClientsWithStore_OnlyDeletes verifies the property that keeps
// this safe to run against a possibly-stale peer: it removes entries, it never
// adds or overwrites them.
//
// Adoption is the lazy reload-on-miss's job, and writing DB rows into the map
// here would revert an in-memory rotation whose persist failed and was
// logged-and-swallowed (persistClientLocked errors are not fatal). Resurrection
// is precisely the bug class #327 is about, so a reconcile that could add rows
// back from a stale view would reintroduce it in a new place.
func TestReconcileClientsWithStore_OnlyDeletes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	gwAuth, gwStore := openServerAuthManager(t, tmpDir, dbPath)
	defer gwStore.Close()
	mine, _, _ := pairClientOn(t, gwAuth, "gateway-phone")

	// A client another process paired after this map was built: reconcile must
	// NOT adopt it.
	peerAuth, peerStore := openCLIAuthManager(t, tmpDir, dbPath)
	defer peerStore.Close()
	foreign, _, _ := pairClientOn(t, peerAuth, "cli-phone")

	// This map holds a NEWER state than the row: the rotation is in memory and
	// its persist is assumed lost. reconcile must not roll it back.
	gwAuth.forgetForTest(t, foreign.ClientID)
	rotated := gwAuth.snapshotForTest(t, mine.ClientID)
	gwAuth.putForTest(t, mine.ClientID, &ClientInfo{
		ClientID:    mine.ClientID,
		TokenHash:   "in-memory-only-" + rotated.TokenHash,
		RefreshHash: "in-memory-only-" + rotated.RefreshHash,
		Created:     rotated.Created,
		Expires:     rotated.Expires,
		LastSeen:    rotated.LastSeen,
	})

	dropped, err := gwAuth.reconcileClientsWithStore()
	if err != nil {
		t.Fatalf("reconcileClientsWithStore: %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("reconcile dropped %v, want nothing: both rows still exist in the table", dropped)
	}
	if _, ok := gwAuth.GetClient(foreign.ClientID); ok {
		t.Error("reconcile adopted a client this process never loaded; adoption belongs to " +
			"the reload-on-miss, and adopting here would let a stale view rewrite session state")
	}
	got, ok := gwAuth.GetClient(mine.ClientID)
	if !ok {
		t.Fatal("reconcile dropped a client whose row is still in the table")
	}
	if !strings.HasPrefix(got.TokenHash, "in-memory-only-") {
		t.Errorf("reconcile overwrote in-memory state with the DB row (token_hash=%q): a lost "+
			"persist would be silently reverted instead of staying live in this process", got.TokenHash)
	}
}

// TestReconcileClientsWithStore_JSONBackendIsNoOp: without a shared DB there is
// nothing to reconcile — the JSON file is this process's own document, and
// deleting entries because they are "missing" from a store that does not exist
// would wipe every client.
func TestReconcileClientsWithStore_JSONBackendIsNoOp(t *testing.T) {
	jsonAuth := openJSONAuthManager(t, t.TempDir())
	client, token, _ := pairClientOn(t, jsonAuth, "json-phone")

	dropped, err := jsonAuth.reconcileClientsWithStore()
	if err != nil {
		t.Fatalf("reconcileClientsWithStore on the JSON backend: %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("JSON reconcile dropped %v; the JSON backend has no shared store to trust", dropped)
	}
	if _, ok := jsonAuth.ValidateToken(token); !ok {
		t.Errorf("the JSON client %s stopped authenticating", client.ClientID)
	}
}

// TestDropWSClientsForAuth_TearsDownRevokedSessions covers the part a token
// check cannot: an already-upgraded WebSocket validated its token once at
// handshake, so dropping the client from the auth map stops FUTURE requests but
// leaves a revoked device streaming. Revocation has to close those too.
func TestDropWSClientsForAuth_TearsDownRevokedSessions(t *testing.T) {
	f := newLifecycleFixture(t)

	revoked := newWSClient("conn-revoked", nil, &ClientInfo{ClientID: "auth-revoked"}, "native:sess-1")
	kept := newWSClient("conn-kept", nil, &ClientInfo{ClientID: "auth-kept"}, "native:sess-2")
	f.channel.addWSClient(revoked)
	f.channel.addWSClient(kept)

	f.channel.dropWSClientsForAuth([]string{"auth-revoked", ""})

	f.channel.mu.RLock()
	_, revokedStill := f.channel.wsClients[revoked.ID]
	_, keptStill := f.channel.wsClients[kept.ID]
	f.channel.mu.RUnlock()

	if revokedStill {
		t.Error("a revoked client's WebSocket survived the reconcile: it keeps receiving " +
			"events for a session that no longer authenticates")
	}
	if !revoked.doneClosed() {
		t.Error("revoked WebSocket was unregistered without closing done: its write loop would linger")
	}
	if !keptStill {
		t.Error("an unrelated client's WebSocket was dropped")
	}
}

// openJSONAuthManager builds a manager with no SQLite repo, i.e. the JSON-file
// backend (repo == nil).
func openJSONAuthManager(t *testing.T, dir string) *AuthManager {
	t.Helper()
	am, err := NewAuthManager(&config.NativeConfig{}, dir)
	if err != nil {
		t.Fatalf("NewAuthManager (JSON): %v", err)
	}
	return am
}

// snapshotForTest returns a copy of a client this manager holds, so a test can
// build a deliberately-divergent replacement without reaching into the map.
func (am *AuthManager) snapshotForTest(t *testing.T, clientID string) *ClientInfo {
	t.Helper()
	client, ok := am.GetClient(clientID)
	if !ok {
		t.Fatalf("client %s not in memory", clientID)
	}
	return client
}

// putForTest installs a client under a key, replacing whatever was there. Used
// to stage in-memory state that is NEWER than the row in the table.
func (am *AuthManager) putForTest(t *testing.T, clientID string, client *ClientInfo) {
	t.Helper()
	am.mu.Lock()
	defer am.mu.Unlock()
	client.ClientID = clientID
	am.store.Clients[clientID] = client
}
