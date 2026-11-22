package channels

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

func openRotationAuthManager(t *testing.T, tmpDir string) *AuthManager {
	t.Helper()

	auth, err := NewAuthManager(defaultTestCfg(), tmpDir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	return auth
}

func openRotationAuthManagerSQLite(t *testing.T, tmpDir, dbPath string) (*AuthManager, *store.Store) {
	t.Helper()

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open(%q): %v", dbPath, err)
	}
	t.Cleanup(func() { s.Close() })

	auth, err := NewAuthManager(defaultTestCfg(), tmpDir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	auth.SetStore(s.NativeClients())
	return auth, s
}

func pairTestClient(t *testing.T, auth *AuthManager, device string) (*ClientInfo, string, string) {
	t.Helper()

	pending, err := auth.GeneratePIN(device)
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}
	client, token, refreshToken, err := auth.PairWithPIN(pending.PIN, device)
	if err != nil {
		t.Fatalf("PairWithPIN: %v", err)
	}
	return client, token, refreshToken
}

func backdateRotation(t *testing.T, am *AuthManager, clientID string, d time.Duration) {
	t.Helper()

	am.mu.Lock()
	defer am.mu.Unlock()

	client, ok := am.store.Clients[clientID]
	if !ok {
		t.Fatalf("backdateRotation: client %s missing from store", clientID)
	}
	client.RotatedAt = time.Now().Add(-d)
}

func requireInvalidRefresh(t *testing.T, err error, context string) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s: expected 'invalid refresh token', got success", context)
	}
	if err.Error() != "invalid refresh token" {
		t.Errorf("%s: expected 'invalid refresh token', got %q", context, err.Error())
	}
}

func spendReloadBudget(t *testing.T, am *AuthManager) {
	t.Helper()

	am.mu.Lock()
	defer am.mu.Unlock()
	am.lastSlowReload = time.Now()
}

func readClientRow(t *testing.T, repo *store.NativeClientRepo, id string) string {
	t.Helper()

	blob, found, err := repo.GetClient(id)
	if err != nil || !found {
		t.Fatalf("GetClient(%s) found=%v err=%v", id, found, err)
	}
	return blob
}

func seedForeignClient(t *testing.T, repo *store.NativeClientRepo, id string) *ClientInfo {
	t.Helper()

	foreign := &ClientInfo{
		ClientID:    id,
		TokenHash:   hashToken(id + "-token"),
		RefreshHash: hashToken(id + "-refresh"),
		DeviceName:  "CLI",
		Created:     time.Now(),
		Expires:     time.Now().Add(24 * time.Hour),
		SessionKeys: []string{id},
	}
	foreignJSON, err := json.Marshal(foreign)
	if err != nil {
		t.Fatalf("marshal foreign client: %v", err)
	}
	if err := repo.SetClient(foreign.ClientID, string(foreignJSON)); err != nil {
		t.Fatalf("seed foreign client: %v", err)
	}
	return foreign
}

func TestRefreshToken_Rotation_SupersededTokenRejectedAfterGrace(t *testing.T) {
	auth := openRotationAuthManager(t, t.TempDir())
	client, _, refresh0 := pairTestClient(t, auth, "grace-device")

	_, token1, refresh1, err := auth.RefreshToken(refresh0)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if refresh1 == refresh0 {
		t.Fatal("rotation must issue a new refresh token")
	}
	if _, valid := auth.ValidateToken(token1); !valid {
		t.Error("rotated access token must validate")
	}

	backdateRotation(t, auth, client.ClientID, rotationGrace+time.Second)

	_, _, _, err = auth.RefreshToken(refresh0)
	requireInvalidRefresh(t, err, "superseded refresh token after the grace window")

	if _, _, _, err := auth.RefreshToken(refresh1); err != nil {
		t.Fatalf("current refresh token must keep working: %v", err)
	}
}

func TestRefreshToken_ReplayWithinGrace_IssuesNewPairOnce(t *testing.T) {
	auth := openRotationAuthManager(t, t.TempDir())
	client, _, refresh0 := pairTestClient(t, auth, "lost-response-device")

	_, token1, refresh1, err := auth.RefreshToken(refresh0)
	if err != nil {
		t.Fatalf("rotation 1: %v", err)
	}

	replayed, token2, refresh2, err := auth.RefreshToken(refresh0)
	if err != nil {
		t.Fatalf("replay inside the grace window must succeed, got: %v", err)
	}
	if replayed == nil || replayed.ClientID != client.ClientID {
		t.Fatalf("replay must return the same client, got %v", replayed)
	}
	if token2 == token1 || refresh2 == refresh1 {
		t.Error("replay must hand out a FRESH pair, not the one that was lost")
	}
	if _, valid := auth.ValidateToken(token2); !valid {
		t.Error("access token issued by the replay must validate")
	}

	_, _, _, err = auth.RefreshToken(refresh0)
	requireInvalidRefresh(t, err, "second replay of a consumed grace token")

	if _, _, _, err := auth.RefreshToken(refresh2); err != nil {
		t.Fatalf("refresh from the replayed pair must work: %v", err)
	}
}

func TestRefreshToken_ReplayWindowBoundary(t *testing.T) {
	auth := openRotationAuthManager(t, t.TempDir())

	outside, _, refreshOutside := pairTestClient(t, auth, "outside-device")
	if _, _, _, err := auth.RefreshToken(refreshOutside); err != nil {
		t.Fatalf("rotation for outside-device: %v", err)
	}
	backdateRotation(t, auth, outside.ClientID, rotationGrace+time.Second)
	_, _, _, err := auth.RefreshToken(refreshOutside)
	requireInvalidRefresh(t, err, "replay just past the grace window")

	inside, _, refreshInside := pairTestClient(t, auth, "inside-device")
	if _, _, _, err := auth.RefreshToken(refreshInside); err != nil {
		t.Fatalf("rotation for inside-device: %v", err)
	}
	backdateRotation(t, auth, inside.ClientID, rotationGrace-5*time.Second)
	if _, _, _, err := auth.RefreshToken(refreshInside); err != nil {
		t.Fatalf("replay %.0fs after rotation must be accepted: %v", (rotationGrace - 5*time.Second).Seconds(), err)
	}
}

func TestRefreshToken_MultiProcess_StaleManagerReloadsAndRotates(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	authA, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	client, _, refreshA := pairTestClient(t, authA, "shared-db-device")

	authB, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	if _, ok := authB.GetClient(client.ClientID); !ok {
		t.Fatal("B must see the client A paired")
	}

	_, tokenA1, refreshA1, err := authA.RefreshToken(refreshA)
	if err != nil {
		t.Fatalf("A: refresh: %v", err)
	}

	_, tokenB, _, err := authB.RefreshToken(refreshA1)
	if err != nil {
		t.Fatalf("B must accept the token A persisted (reload on miss), got: %v", err)
	}

	if _, valid := authA.ValidateToken(tokenB); !valid {
		t.Error("A must validate the token B issued (directed write + reload on miss)")
	}

	authC, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	if _, valid := authC.ValidateToken(tokenB); !valid {
		t.Error("a restarted process must resolve the live token")
	}
	if _, valid := authC.ValidateToken(tokenA1); valid {
		t.Error("A's superseded token must not survive in the DB")
	}
}

func TestRefreshToken_PersistsOnlyTheRotatedRow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")
	auth, s := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	repo := s.NativeClients()

	rotated, _, rotatedRefresh := pairTestClient(t, auth, "rotated-device")
	untouched, _, _ := pairTestClient(t, auth, "untouched-device")

	foreign := seedForeignClient(t, repo, "foreign-cli")

	beforeRotated := readClientRow(t, repo, rotated.ClientID)
	beforeUntouched := readClientRow(t, repo, untouched.ClientID)
	beforeForeign := readClientRow(t, repo, foreign.ClientID)

	_, _, newRefresh, err := auth.RefreshToken(rotatedRefresh)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if after := readClientRow(t, repo, untouched.ClientID); after != beforeUntouched {
		t.Errorf("untouched client row changed:\n before=%s\n after =%s", beforeUntouched, after)
	}
	if after := readClientRow(t, repo, foreign.ClientID); after != beforeForeign {
		t.Errorf("a client unknown to this manager was rewritten/deleted:\n before=%s\n after =%s", beforeForeign, after)
	}

	afterRotated := readClientRow(t, repo, rotated.ClientID)
	if afterRotated == beforeRotated {
		t.Fatal("the rotated row was not persisted")
	}

	var stored ClientInfo
	if err := json.Unmarshal([]byte(afterRotated), &stored); err != nil {
		t.Fatalf("unmarshal rotated row: %v", err)
	}
	if stored.RefreshHash != hashToken(newRefresh) {
		t.Error("persisted row does not carry the new refresh hash")
	}
	if stored.PrevRefreshHash != hashToken(rotatedRefresh) {
		t.Errorf("persisted PrevRefreshHash = %q, want the superseded hash %q",
			stored.PrevRefreshHash, hashToken(rotatedRefresh))
	}
	if stored.RotatedAt.IsZero() {
		t.Error("persisted RotatedAt must be set by the rotation")
	}
}

func TestRefreshToken_LegacyClientBlobWithoutRotationFields(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	legacy := `{"client_id":"legacy-cli","token_hash":"` + hashToken("legacy-token") +
		`","refresh_hash":"` + hashToken("legacy-refresh") +
		`","device_name":"Legacy","created":"2024-01-01T00:00:00Z",` +
		`"expires":"2999-01-01T00:00:00Z","last_seen":"2024-01-01T00:00:00Z",` +
		`"session_keys":["legacy-cli"]}`

	seed, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer seed.Close()
	if err := seed.NativeClients().SetClient("legacy-cli", legacy); err != nil {
		t.Fatalf("seed legacy client: %v", err)
	}

	auth, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)

	got, ok := auth.GetClient("legacy-cli")
	if !ok {
		t.Fatal("legacy client must load")
	}
	if got.PrevRefreshHash != "" || got.PrevTokenHash != "" || !got.RotatedAt.IsZero() {
		t.Errorf("legacy client must have an empty rotation history, got %+v", got)
	}
	if _, valid := auth.ValidateToken("legacy-token"); !valid {
		t.Error("legacy access token must still validate")
	}

	if _, _, _, err := auth.RefreshToken("legacy-refresh"); err != nil {
		t.Fatalf("legacy refresh token: %v", err)
	}

	blob := readClientRow(t, seed.NativeClients(), "legacy-cli")
	for _, key := range []string{`"prev_refresh_hash"`, `"prev_token_hash"`, `"rotated_at"`} {
		if !strings.Contains(blob, key) {
			t.Errorf("persisted blob is missing %s: %s", key, blob)
		}
	}

	if _, _, _, err := auth.RefreshToken("legacy-refresh"); err != nil {
		t.Fatalf("legacy refresh token must be retryable inside the grace window: %v", err)
	}

	if blob := readClientRow(t, seed.NativeClients(), "legacy-cli"); strings.Contains(blob, `"prev_refresh_hash"`) ||
		strings.Contains(blob, `"prev_token_hash"`) {
		t.Errorf("the replay must consume the grace, but the blob still carries Prev*: %s", blob)
	}

	if _, _, _, err := auth.RefreshToken("legacy-refresh"); err == nil {
		t.Error("the consuming manager must not issue a third pair for a consumed grace")
	}

	authAfterConsume, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	_, _, _, replayErr := authAfterConsume.RefreshToken("legacy-refresh")
	requireInvalidRefresh(t, replayErr, "legacy token after its grace was consumed")
}

func TestValidateToken_SlowPathReloadIsThrottled(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	authA, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	_, _, refresh0 := pairTestClient(t, authA, "throttle-device")

	authB, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)

	_, token1, refresh1, err := authA.RefreshToken(refresh0)
	if err != nil {
		t.Fatalf("A: refresh: %v", err)
	}

	if _, valid := authB.ValidateToken(token1); !valid {
		t.Fatal("the FIRST slow-path miss must reload and adopt another process's token")
	}

	authD, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)

	_, token2, _, err := authA.RefreshToken(refresh1)
	if err != nil {
		t.Fatalf("A: second refresh: %v", err)
	}

	spendReloadBudget(t, authB)

	if _, valid := authB.ValidateToken(token2); valid {
		t.Error("a second slow-path miss inside slowReloadMinInterval must NOT reload the store")
	}

	if _, valid := authD.ValidateToken(token2); !valid {
		t.Error("a manager with an unspent budget must resolve the token the throttled miss skipped")
	}
}

func TestRefreshToken_SuppressedReloadReturnsUnavailableNotInvalid(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	authA, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	client, _, refresh0 := pairTestClient(t, authA, "budget-device")

	authThrottled, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	authControl, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	if _, ok := authThrottled.GetClient(client.ClientID); !ok {
		t.Fatal("the throttled manager must know the client before the rotation")
	}

	spendReloadBudget(t, authThrottled)

	_, _, refresh1, err := authA.RefreshToken(refresh0)
	if err != nil {
		t.Fatalf("A: refresh: %v", err)
	}

	_, _, _, err = authThrottled.RefreshToken(refresh1)
	if err == nil {
		t.Fatal("a suppressed reload must not accept a token it never checked")
	}
	if !errors.Is(err, errRefreshUnavailable) {
		t.Fatalf("suppressed reload must return errRefreshUnavailable, got %q (400/refresh_error here expels a healthy session)", err)
	}

	_, controlToken, refresh2, err := authControl.RefreshToken(refresh1)
	if err != nil {
		t.Fatalf("a manager with an intact budget must adopt the rotation instead of rejecting it, got: %v", err)
	}
	if _, valid := authControl.ValidateToken(controlToken); !valid {
		t.Error("the pair the control process issued must validate")
	}

	if _, _, _, err := authA.RefreshToken(refresh2); err != nil {
		t.Fatalf("A must adopt the rotation another process persisted: %v", err)
	}

	authFresh, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	_, _, _, err = authFresh.RefreshToken("no-such-refresh-token")
	requireInvalidRefresh(t, err, "unknown token on a manager with an intact budget")
}

func TestRefreshToken_RowWithDivergentClientIDStillRotates(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	manual := `{"client_id":"ghost-id","token_hash":"` + hashToken("manual-token") +
		`","refresh_hash":"` + hashToken("manual-refresh") +
		`","device_name":"Manual","created":"2024-01-01T00:00:00Z",` +
		`"expires":"2999-01-01T00:00:00Z","last_seen":"2024-01-01T00:00:00Z",` +
		`"session_keys":["manual-cli"]}`

	seed, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer seed.Close()
	if err := seed.NativeClients().SetClient("manual-cli", manual); err != nil {
		t.Fatalf("seed manual row: %v", err)
	}

	auth, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)

	got, ok := auth.GetClient("manual-cli")
	if !ok {
		t.Fatal("the manual row must load under its key")
	}
	if got.ClientID != "manual-cli" {
		t.Fatalf("loadStore must stamp the map key onto ClientID, got %q", got.ClientID)
	}

	_, _, newRefresh, err := auth.RefreshToken("manual-refresh")
	if err != nil {
		t.Fatalf("refresh of a row with a divergent client_id: %v", err)
	}

	var stored ClientInfo
	if err := json.Unmarshal([]byte(readClientRow(t, seed.NativeClients(), "manual-cli")), &stored); err != nil {
		t.Fatalf("unmarshal rotated row: %v", err)
	}
	if stored.RefreshHash != hashToken(newRefresh) {
		t.Error("the rotation was not persisted for the row (persistClientLocked missed it)")
	}
	if stored.ClientID != "manual-cli" {
		t.Errorf("the persisted row must name its key, got %q", stored.ClientID)
	}

	authRestarted, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	if _, _, _, err := authRestarted.RefreshToken(newRefresh); err != nil {
		t.Fatalf("after a restart the rotated pair must still work: %v", err)
	}
}

func TestTrackSessionKey_DoesNotRevertForeignRotations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	authA, s := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	repo := s.NativeClients()

	rotated, _, rotatedRefreshA := pairTestClient(t, authA, "rotated-device")
	tracked, _, _ := pairTestClient(t, authA, "tracked-device")

	foreign := seedForeignClient(t, repo, "foreign-cli")

	authB, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	if _, ok := authB.GetClient(rotated.ClientID); !ok {
		t.Fatal("B must see the client A paired")
	}
	if _, ok := authB.GetClient(tracked.ClientID); !ok {
		t.Fatal("B must see the tracked client A paired")
	}

	_, _, rotatedRefreshA1, err := authA.RefreshToken(rotatedRefreshA)
	if err != nil {
		t.Fatalf("A: refresh: %v", err)
	}
	beforeRotated := readClientRow(t, repo, rotated.ClientID)
	beforeForeign := readClientRow(t, repo, foreign.ClientID)

	authB.TrackSessionKey(tracked.ClientID, "webui-session-key")

	if after := readClientRow(t, repo, rotated.ClientID); after != beforeRotated {
		t.Errorf("TrackSessionKey rewrote a client it never touched:\n before=%s\n after =%s",
			beforeRotated, after)
	}
	var storedRotated ClientInfo
	if err := json.Unmarshal([]byte(readClientRow(t, repo, rotated.ClientID)), &storedRotated); err != nil {
		t.Fatalf("unmarshal rotated row: %v", err)
	}
	if storedRotated.RefreshHash != hashToken(rotatedRefreshA1) {
		t.Error("TrackSessionKey reverted the rotation process A had persisted (expelled client reappears)")
	}

	if after := readClientRow(t, repo, foreign.ClientID); after != beforeForeign {
		t.Errorf("TrackSessionKey rewrote/deleted a client it does not know:\n before=%s\n after =%s",
			beforeForeign, after)
	}

	var storedTracked ClientInfo
	if err := json.Unmarshal([]byte(readClientRow(t, repo, tracked.ClientID)), &storedTracked); err != nil {
		t.Fatalf("unmarshal tracked row: %v", err)
	}
	found := false
	for _, key := range storedTracked.SessionKeys {
		if key == "webui-session-key" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("the tracked session key was not persisted, got %v", storedTracked.SessionKeys)
	}
}

func TestRefreshToken_ReplayGraceIsSingleUseAcrossProcesses(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	authA, s := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	repo := s.NativeClients()

	client, _, refresh0 := pairTestClient(t, authA, "shared-grace-device")

	if _, _, _, err := authA.RefreshToken(refresh0); err != nil {
		t.Fatalf("A: rotation 1: %v", err)
	}

	authB, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	if _, ok := authB.GetClient(client.ClientID); !ok {
		t.Fatal("B must see the client A rotated")
	}

	replayed, _, refresh2, err := authB.RefreshToken(refresh0)
	if err != nil {
		t.Fatalf("the replay inside the grace must be accepted once, got: %v", err)
	}
	if replayed == nil || replayed.ClientID != client.ClientID {
		t.Fatalf("the replay must return the same client, got %v", replayed)
	}

	var stored ClientInfo
	if err := json.Unmarshal([]byte(readClientRow(t, repo, client.ClientID)), &stored); err != nil {
		t.Fatalf("unmarshal rotated row: %v", err)
	}
	if stored.PrevRefreshHash != "" || stored.PrevTokenHash != "" {
		t.Errorf("a consumed grace must leave no replay material on the row, got prev_refresh_hash=%q prev_token_hash=%q",
			stored.PrevRefreshHash, stored.PrevTokenHash)
	}
	if stored.RefreshHash != hashToken(refresh2) {
		t.Error("the row must carry the pair the replay issued")
	}

	authC, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	_, _, _, err = authC.RefreshToken(refresh0)
	requireInvalidRefresh(t, err, "replay of a consumed grace in a third process")

	if _, _, _, err := authC.RefreshToken(refresh2); err != nil {
		t.Fatalf("the pair issued by the replay must survive: %v", err)
	}
}

func TestSingleClientMutators_DoNotTouchForeignRows(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	auth, s := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	repo := s.NativeClients()

	sessionClient, _, _ := pairTestClient(t, auth, "session-device")
	doomed, _, _ := pairTestClient(t, auth, "revoked-device")
	foreign := seedForeignClient(t, repo, "foreign-cli")
	beforeForeign := readClientRow(t, repo, foreign.ClientID)

	mutations := []struct {
		name string
		run  func()
	}{
		{"TrackSessionKey", func() { auth.TrackSessionKey(sessionClient.ClientID, "session-key-1") }},
		{"RemoveSessionKey", func() {
			if err := auth.RemoveSessionKey(sessionClient.ClientID, "session-key-1"); err != nil {
				t.Fatalf("RemoveSessionKey: %v", err)
			}
		}},
		{"RegisterDesktopClient", func() {
			if err := auth.RegisterDesktopClient("desktop-token", "desktop-refresh"); err != nil {
				t.Fatalf("RegisterDesktopClient: %v", err)
			}
		}},
		{"RemoveClient", func() {
			if err := auth.RemoveClient(doomed.ClientID); err != nil {
				t.Fatalf("RemoveClient: %v", err)
			}
		}},
		{"PairWithPIN", func() {
			pending, err := auth.GeneratePIN("late-device")
			if err != nil {
				t.Fatalf("GeneratePIN: %v", err)
			}
			if _, _, _, err := auth.PairWithPIN(pending.PIN, "late-device"); err != nil {
				t.Fatalf("PairWithPIN: %v", err)
			}
		}},
	}

	for i, mutation := range mutations {
		mutation.run()

		after, found, err := repo.GetClient(foreign.ClientID)
		if err != nil || !found {
			t.Fatalf("step %d (%s): a client unknown to this manager was DELETED by the mutation (found=%v err=%v)",
				i+1, mutation.name, found, err)
		}
		if after != beforeForeign {
			t.Fatalf("step %d (%s): a client unknown to this manager was rewritten:\n before=%s\n after =%s",
				i+1, mutation.name, beforeForeign, after)
		}
	}

	var sessionStored ClientInfo
	if err := json.Unmarshal([]byte(readClientRow(t, repo, sessionClient.ClientID)), &sessionStored); err != nil {
		t.Fatalf("unmarshal session client row: %v", err)
	}
	if len(sessionStored.SessionKeys) != 1 || sessionStored.SessionKeys[0] != sessionClient.ClientID {
		t.Errorf("TrackSessionKey/RemoveSessionKey did not round-trip, got %v", sessionStored.SessionKeys)
	}
	readClientRow(t, repo, DesktopClientID)
	if _, found, err := repo.GetClient(doomed.ClientID); err != nil || found {
		t.Errorf("RemoveClient must delete the revoked row, found=%v err=%v", found, err)
	}
	if n, err := repo.CountClients(); err != nil {
		t.Fatalf("CountClients: %v", err)
	} else if n != 4 {
		t.Errorf("client row count = %d, want 4 (foreign, session, desktop, late)", n)
	}
}
