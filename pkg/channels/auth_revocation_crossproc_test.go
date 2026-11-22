package channels

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

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

func TestRevocation_GatewayWriteDoesNotResurrectClientRemovedByCLI(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	gwAuth, gwStore := openServerAuthManager(t, tmpDir, dbPath)
	defer gwStore.Close()

	revoked, _, _ := pairClientOn(t, gwAuth, "revoked-phone")
	kept, _, _ := pairClientOn(t, gwAuth, "kept-phone")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	defer cliStore.Close()
	if err := cliAuth.RemoveClient(revoked.ClientID); err != nil {
		t.Fatalf("CLI RemoveClient: %v", err)
	}
	if ids := dbClientIDs(t, gwStore); ids[revoked.ClientID] {
		t.Fatalf("precondition: CLI removal did not reach the DB (rows: %v)", ids)
	}

	if _, ok := gwAuth.GetClient(revoked.ClientID); !ok {
		t.Fatal("precondition: the gateway map must still hold the revoked client, " +
			"otherwise this test does not exercise the stale-snapshot path")
	}

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

}

func TestRevocation_CLIWriteDoesNotDeleteClientsItNeverLoaded(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	defer cliStore.Close()
	early, _, _ := pairClientOn(t, cliAuth, "early-phone")

	gwAuth, gwStore := openServerAuthManager(t, tmpDir, dbPath)
	defer gwStore.Close()
	later, _, _ := pairClientOn(t, gwAuth, "later-phone")

	if _, ok := cliAuth.GetClient(later.ClientID); ok {
		t.Fatal("precondition: the CLI map must be stale (it must not know the later client)")
	}

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

func TestSaveStoreUnlocked_StillClobbersAcrossProcesses(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	first, firstStore := openServerAuthManager(t, tmpDir, dbPath)
	defer firstStore.Close()
	clientA, _, _ := pairClientOn(t, first, "device-a")

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

func (am *AuthManager) forgetForTest(t *testing.T, clientID string) {
	t.Helper()
	am.mu.Lock()
	defer am.mu.Unlock()
	delete(am.store.Clients, clientID)
}

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

	if _, ok := gwAuth.ValidateToken(keptToken); !ok {
		t.Errorf("reconcile dropped the unrelated client %s", kept.ClientID)
	}
}

func TestReconcileClientsWithStore_OnlyDeletes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	gwAuth, gwStore := openServerAuthManager(t, tmpDir, dbPath)
	defer gwStore.Close()
	mine, _, _ := pairClientOn(t, gwAuth, "gateway-phone")

	peerAuth, peerStore := openCLIAuthManager(t, tmpDir, dbPath)
	defer peerStore.Close()
	foreign, _, _ := pairClientOn(t, peerAuth, "cli-phone")

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

func openJSONAuthManager(t *testing.T, dir string) *AuthManager {
	t.Helper()
	am, err := NewAuthManager(&config.NativeConfig{}, dir)
	if err != nil {
		t.Fatalf("NewAuthManager (JSON): %v", err)
	}
	return am
}

func (am *AuthManager) snapshotForTest(t *testing.T, clientID string) *ClientInfo {
	t.Helper()
	client, ok := am.GetClient(clientID)
	if !ok {
		t.Fatalf("client %s not in memory", clientID)
	}
	return client
}

func (am *AuthManager) putForTest(t *testing.T, clientID string, client *ClientInfo) {
	t.Helper()
	am.mu.Lock()
	defer am.mu.Unlock()
	client.ClientID = clientID
	am.store.Clients[clientID] = client
}
