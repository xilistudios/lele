package channels

// auth_rotation_grace_test.go — guards the two causes of a FALSE WebUI logout
// in the refresh path:
//
//	A. the rotation is single use, so a refresh response lost in the network
//	   left the client holding a credential the server had already replaced:
//	   it got "invalid refresh token" and the frontend expelled itself while
//	   the server-side client record was perfectly alive;
//	B. multi-process: the gateway keeps am.store.Clients in memory, so a
//	   client rotated/persisted by another process (CLI, TUI, a second
//	   gateway sharing the same lele.db) can be missing from this manager's
//	   copy — and the refresh path used to persist with saveStoreUnlocked,
//	   which upserts the WHOLE map and DELETEs table rows absent from it,
//	   i.e. it reverted the other process's rotation.
//
// The tests drive the real SQLite repo/JSON store rather than mocks, and
// backdate RotatedAt instead of sleeping through the 60s grace window.

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

// openRotationAuthManager builds a manager on the JSON backend (repo == nil).
func openRotationAuthManager(t *testing.T, tmpDir string) *AuthManager {
	t.Helper()

	auth, err := NewAuthManager(defaultTestCfg(), tmpDir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	return auth
}

// openRotationAuthManagerSQLite builds a manager over its OWN store handle on
// dbPath — the wiring used by cmd/lele and by two gateways side by side: one
// process = one handle = one in-memory map that goes stale against the DB.
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

// pairTestClient pairs a device through the manager's own PIN flow.
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

// backdateRotation moves a client's last rotation d into the past, so the
// grace window can be exercised without sleeping. Test-only backdoor: the
// field is only ever written under am.mu.
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

// requireInvalidRefresh is the shared assertion for the rejection path.
func requireInvalidRefresh(t *testing.T, err error, context string) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s: expected 'invalid refresh token', got success", context)
	}
	if err.Error() != "invalid refresh token" {
		t.Errorf("%s: expected 'invalid refresh token', got %q", context, err.Error())
	}
}

// spendReloadBudget forces the manager's slow-path reload budget to be spent
// "now", so a test can assert the SUPPRESSED path without racing the clock:
// relying on a real call having happened less than slowReloadMinInterval ago
// makes the assertion flaky on a loaded CI (a SQLite transaction + store.Open
// + loadStore in between can easily exceed 250ms). Test-only backdoor: the
// field is only ever written under am.mu.
func spendReloadBudget(t *testing.T, am *AuthManager) {
	t.Helper()

	am.mu.Lock()
	defer am.mu.Unlock()
	am.lastSlowReload = time.Now()
}

// readClientRow returns the blob persisted for id, failing the test when the
// row is missing — the direct way to observe what a mutator wrote to the
// shared DB (as opposed to what some process keeps in memory).
func readClientRow(t *testing.T, repo *store.NativeClientRepo, id string) string {
	t.Helper()

	blob, found, err := repo.GetClient(id)
	if err != nil || !found {
		t.Fatalf("GetClient(%s) found=%v err=%v", id, found, err)
	}
	return blob
}

// seedForeignClient persists a client row directly into the DB: a client that
// exists only in the shared store, as if another process had just paired it.
// A whole-store save from a manager that does not know this id DELETEs it.
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

// ---------------------------------------------------------------------------
// T-a: normal rotation; the superseded refresh token dies with the grace
// ---------------------------------------------------------------------------

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

	// Inside the grace window the superseded token is still usable (T-b);
	// push the rotation out of the window to prove it is not usable forever.
	backdateRotation(t, auth, client.ClientID, rotationGrace+time.Second)

	_, _, _, err = auth.RefreshToken(refresh0)
	requireInvalidRefresh(t, err, "superseded refresh token after the grace window")

	// The current pair is untouched by the rejected attempt.
	if _, _, _, err := auth.RefreshToken(refresh1); err != nil {
		t.Fatalf("current refresh token must keep working: %v", err)
	}
}

// ---------------------------------------------------------------------------
// T-b: replay inside the grace hands out a new pair, exactly once
// ---------------------------------------------------------------------------

func TestRefreshToken_ReplayWithinGrace_IssuesNewPairOnce(t *testing.T) {
	auth := openRotationAuthManager(t, t.TempDir())
	client, _, refresh0 := pairTestClient(t, auth, "lost-response-device")

	// Rotation 1: the server replaced both hashes...
	_, token1, refresh1, err := auth.RefreshToken(refresh0)
	if err != nil {
		t.Fatalf("rotation 1: %v", err)
	}

	// ...and the client never saw it, so it retries with the token it still
	// holds. This is the lost-response case that used to expel the session.
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

	// The grace is consumed BY the replay: the replay clears Prev* on the
	// persisted row (global single-use, so no other process can spend this
	// token either), which is why the token the client used cannot buy a
	// third pair.
	_, _, _, err = auth.RefreshToken(refresh0)
	requireInvalidRefresh(t, err, "second replay of a consumed grace token")

	// The live pair is the one the replay issued.
	if _, _, _, err := auth.RefreshToken(refresh2); err != nil {
		t.Fatalf("refresh from the replayed pair must work: %v", err)
	}
}

// ---------------------------------------------------------------------------
// T-c: the grace window boundary (inclusive) and beyond it
// ---------------------------------------------------------------------------

func TestRefreshToken_ReplayWindowBoundary(t *testing.T) {
	auth := openRotationAuthManager(t, t.TempDir())

	// Just OUTSIDE the window → rejected. A previous token showing up after
	// the window is a replay of a live-era credential, not a retry.
	outside, _, refreshOutside := pairTestClient(t, auth, "outside-device")
	if _, _, _, err := auth.RefreshToken(refreshOutside); err != nil {
		t.Fatalf("rotation for outside-device: %v", err)
	}
	backdateRotation(t, auth, outside.ClientID, rotationGrace+time.Second)
	_, _, _, err := auth.RefreshToken(refreshOutside)
	requireInvalidRefresh(t, err, "replay just past the grace window")

	// Just INSIDE the window → accepted (rotationGrace is inclusive).
	inside, _, refreshInside := pairTestClient(t, auth, "inside-device")
	if _, _, _, err := auth.RefreshToken(refreshInside); err != nil {
		t.Fatalf("rotation for inside-device: %v", err)
	}
	backdateRotation(t, auth, inside.ClientID, rotationGrace-5*time.Second)
	if _, _, _, err := auth.RefreshToken(refreshInside); err != nil {
		t.Fatalf("replay %.0fs after rotation must be accepted: %v", (rotationGrace - 5*time.Second).Seconds(), err)
	}
}

// ---------------------------------------------------------------------------
// T-d: multi-process — a stale manager must reload instead of rejecting
// ---------------------------------------------------------------------------

func TestRefreshToken_MultiProcess_StaleManagerReloadsAndRotates(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	// Process A pairs the device.
	authA, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	client, _, refreshA := pairTestClient(t, authA, "shared-db-device")

	// Process B starts afterwards and freezes its own copy of the client.
	authB, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	if _, ok := authB.GetClient(client.ClientID); !ok {
		t.Fatal("B must see the client A paired")
	}

	// A rotates while B's map stays stale.
	_, tokenA1, refreshA1, err := authA.RefreshToken(refreshA)
	if err != nil {
		t.Fatalf("A: refresh: %v", err)
	}

	// B receives the refresh token A just issued: valid on disk (and in B's
	// DB), unknown to B's in-memory copy. B must reload, not expel.
	_, tokenB, _, err := authB.RefreshToken(refreshA1)
	if err != nil {
		t.Fatalf("B must accept the token A persisted (reload on miss), got: %v", err)
	}

	// B's rotation was persisted with a DIRECTED write, so A resolves the
	// live token via ValidateToken's own reload-on-miss.
	if _, valid := authA.ValidateToken(tokenB); !valid {
		t.Error("A must validate the token B issued (directed write + reload on miss)")
	}

	// A fresh process sees exactly the pair B issued.
	authC, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	if _, valid := authC.ValidateToken(tokenB); !valid {
		t.Error("a restarted process must resolve the live token")
	}
	if _, valid := authC.ValidateToken(tokenA1); valid {
		t.Error("A's superseded token must not survive in the DB")
	}
}

// ---------------------------------------------------------------------------
// T-e: the rotation writes ONLY the rotated row (no full-store upsert/delete)
// ---------------------------------------------------------------------------

func TestRefreshToken_PersistsOnlyTheRotatedRow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")
	auth, s := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	repo := s.NativeClients()

	rotated, _, rotatedRefresh := pairTestClient(t, auth, "rotated-device")
	untouched, _, _ := pairTestClient(t, auth, "untouched-device")

	// A row this manager knows nothing about — as if another process had just
	// paired it against the same DB. saveStoreUnlocked would DELETE it.
	foreign := seedForeignClient(t, repo, "foreign-cli")

	beforeRotated := readClientRow(t, repo, rotated.ClientID)
	beforeUntouched := readClientRow(t, repo, untouched.ClientID)
	beforeForeign := readClientRow(t, repo, foreign.ClientID)

	_, _, newRefresh, err := auth.RefreshToken(rotatedRefresh)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// The rotated row is the only one written.
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

// ---------------------------------------------------------------------------
// Compatibility: blobs written before the rotation fields existed
// ---------------------------------------------------------------------------

func TestRefreshToken_LegacyClientBlobWithoutRotationFields(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	// Exactly the shape an older build persisted: no prev_* / rotated_at.
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

	// It deserialises with an empty rotation history, so the grace window has
	// nothing to match until this client is rotated again.
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

	// Its refresh token still works, and the first rotation performed by this
	// code records the legacy hashes — so the grace window is available from
	// then on.
	if _, _, _, err := auth.RefreshToken("legacy-refresh"); err != nil {
		t.Fatalf("legacy refresh token: %v", err)
	}

	// The persisted blob carries the new snake_case keys.
	blob := readClientRow(t, seed.NativeClients(), "legacy-cli")
	for _, key := range []string{`"prev_refresh_hash"`, `"prev_token_hash"`, `"rotated_at"`} {
		if !strings.Contains(blob, key) {
			t.Errorf("persisted blob is missing %s: %s", key, blob)
		}
	}

	// The same legacy token is still retryable inside the grace window (the
	// lost-response case)...
	if _, _, _, err := auth.RefreshToken("legacy-refresh"); err != nil {
		t.Fatalf("legacy refresh token must be retryable inside the grace window: %v", err)
	}

	// ...and that retry CONSUMES the grace on the row itself: the persisted
	// blob no longer names a previous hash, so no other process can replay
	// this legacy token a second time either. omitempty makes the consumed
	// fields disappear from the JSON.
	if blob := readClientRow(t, seed.NativeClients(), "legacy-cli"); strings.Contains(blob, `"prev_refresh_hash"`) ||
		strings.Contains(blob, `"prev_token_hash"`) {
		t.Errorf("the replay must consume the grace, but the blob still carries Prev*: %s", blob)
	}

	// The consuming manager refuses the token too — in memory the grace is
	// gone as well — so no third pair is issued. The FAILURE KIND is not
	// asserted here on purpose: if this attempt lands inside the manager's
	// slow-path budget the refusal is the retryable sentinel (the store was
	// not consulted), which is the intended behaviour of the throttle.
	if _, _, _, err := auth.RefreshToken("legacy-refresh"); err == nil {
		t.Error("the consuming manager must not issue a third pair for a consumed grace")
	}

	// The row's state is what makes the token dead for EVERY process, so the
	// fatal identity is asserted from a manager that loads the store after the
	// consume: its budget is intact, it really consults the DB, finds no
	// current and no previous hash and rejects with "invalid refresh token".
	authAfterConsume, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	_, _, _, replayErr := authAfterConsume.RefreshToken("legacy-refresh")
	requireInvalidRefresh(t, replayErr, "legacy token after its grace was consumed")
}

// ---------------------------------------------------------------------------
// T-f: M3 — the slow-path reload is budgeted per manager
// ---------------------------------------------------------------------------

// A cache miss in ValidateToken is reachable with NO credential (an invalid
// token matches nothing) and each miss used to run a full loadStore — a SELECT
// of every client blob plus one unmarshal per row — under the EXCLUSIVE
// am.mu, i.e. one unauthenticated request could force that work and serialise
// every authenticated request behind it. The budget must bound that without
// breaking the reload-on-miss the multi-process setup depends on.
func TestValidateToken_SlowPathReloadIsThrottled(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	authA, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	_, _, refresh0 := pairTestClient(t, authA, "throttle-device")

	// B freezes a private copy of the store before A rotates: A's tokens are
	// then invisible to B until B reloads.
	authB, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)

	_, token1, refresh1, err := authA.RefreshToken(refresh0)
	if err != nil {
		t.Fatalf("A: refresh: %v", err)
	}

	// First miss on B: its budget is untouched (lastSlowReload is zero), so
	// the reload MUST run and adopt the token A just persisted. This is the
	// property the budget must never break.
	if _, valid := authB.ValidateToken(token1); !valid {
		t.Fatal("the FIRST slow-path miss must reload and adopt another process's token")
	}

	// D loads NOW, i.e. before A's next rotation: D's copy knows token1 but
	// not token2, so resolving token2 later forces D through its slow path
	// (a genuine reload, not a hot-path map hit). Its budget is untouched.
	authD, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)

	// A rotates again, so token2 is now visible only to A and in the DB.
	_, token2, _, err := authA.RefreshToken(refresh1)
	if err != nil {
		t.Fatalf("A: second refresh: %v", err)
	}

	// Force B's budget to be spent "now" instead of relying on the previous
	// miss having been less than slowReloadMinInterval ago: the steps in
	// between do real SQLite work (+ store.Open), so on a loaded CI more than
	// 250ms can elapse and the assertion below would flake.
	spendReloadBudget(t, authB)

	// The miss on B, with its budget just spent, must NOT reload: B keeps
	// answering from its stale copy (which still names token1).
	if _, valid := authB.ValidateToken(token2); valid {
		t.Error("a second slow-path miss inside slowReloadMinInterval must NOT reload the store")
	}

	// D proves the token is real and that a reload (not the token) is what B
	// was denied: D's first miss reloads and adopts it.
	if _, valid := authD.ValidateToken(token2); !valid {
		t.Error("a manager with an unspent budget must resolve the token the throttled miss skipped")
	}
}

// ---------------------------------------------------------------------------
// T-f2: MAJOR — a suppressed reload must NOT be reported as an invalid credential
// ---------------------------------------------------------------------------

// When the slow-path budget is spent, RefreshToken cannot consult the shared
// store. Answering "invalid refresh token" for that case is FATAL for a WebUI
// (isFatalRefreshFailure(400) → clearSession → re-pairing) even though the
// credential may be perfectly valid in the DB — typically because another
// process rotated it inside slowReloadMinInterval. The suppressed case must be
// reported as errRefreshUnavailable instead, which handleRefresh maps to
// 429 + Retry-After: a status the frontend handles as non-fatal and retries.
// The throttle must not reintroduce the very logout this PR removes.
func TestRefreshToken_SuppressedReloadReturnsUnavailableNotInvalid(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	authA, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	client, _, refresh0 := pairTestClient(t, authA, "budget-device")

	// Two more processes freeze the pre-rotation state: neither knows the pair
	// A is about to issue, exactly like a gateway whose map went stale.
	authThrottled, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	authControl, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	if _, ok := authThrottled.GetClient(client.ClientID); !ok {
		t.Fatal("the throttled manager must know the client before the rotation")
	}

	// Spend the throttled manager's budget: its next miss will NOT reload.
	spendReloadBudget(t, authThrottled)

	// A rotates; the new refresh token lives only in A's map and in the DB.
	_, _, refresh1, err := authA.RefreshToken(refresh0)
	if err != nil {
		t.Fatalf("A: refresh: %v", err)
	}

	// The throttled manager must report "could not check", NOT "your
	// credential is invalid".
	_, _, _, err = authThrottled.RefreshToken(refresh1)
	if err == nil {
		t.Fatal("a suppressed reload must not accept a token it never checked")
	}
	if !errors.Is(err, errRefreshUnavailable) {
		t.Fatalf("suppressed reload must return errRefreshUnavailable, got %q (400/refresh_error here expels a healthy session)", err)
	}

	// Control on the SAME frozen map with one difference: its budget is
	// intact. It reloads, adopts A's rotation and hands out a fresh pair.
	_, controlToken, refresh2, err := authControl.RefreshToken(refresh1)
	if err != nil {
		t.Fatalf("a manager with an intact budget must adopt the rotation instead of rejecting it, got: %v", err)
	}
	if _, valid := authControl.ValidateToken(controlToken); !valid {
		t.Error("the pair the control process issued must validate")
	}

	// The throttle consumed nothing: A adopts the pair the control process
	// persisted on its own next miss.
	if _, _, _, err := authA.RefreshToken(refresh2); err != nil {
		t.Fatalf("A must adopt the rotation another process persisted: %v", err)
	}

	// The FATAL path is preserved: a manager that DID consult the store and
	// found nothing still rejects with "invalid refresh token" (400), never
	// with the retryable sentinel.
	authFresh, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	_, _, _, err = authFresh.RefreshToken("no-such-refresh-token")
	requireInvalidRefresh(t, err, "unknown token on a manager with an intact budget")
}

// ---------------------------------------------------------------------------
// T-f3: MINOR — the MAP KEY, never the blob's client_id, identifies the row
// ---------------------------------------------------------------------------

// A migrated/hand-edited row can carry a client_id that is empty or different
// from its key. loadStore used to trust the blob, so the in-memory client was
// filed under an id its own struct did not name — and rotateClientLocked
// persisted by client.ClientID, landing on a client that does not exist in the
// map: persistClientLocked returned nil in silence and the ROTATION WAS LOST.
// A gateway restart then reloaded the old row and expelled the client. Both
// ends now agree: loadStore stamps the key onto the field, and the rotation
// writes under the key it was given.
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

	// The key wins: the loaded client must nominate the id it is filed under.
	got, ok := auth.GetClient("manual-cli")
	if !ok {
		t.Fatal("the manual row must load under its key")
	}
	if got.ClientID != "manual-cli" {
		t.Fatalf("loadStore must stamp the map key onto ClientID, got %q", got.ClientID)
	}

	// The rotation must be PERSISTED (not silently dropped for a phantom id).
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

	// A restart (a fresh process over the same DB) sees the live pair, i.e.
	// the client is not expelled by the reload.
	authRestarted, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	if _, _, _, err := authRestarted.RefreshToken(newRefresh); err != nil {
		t.Fatalf("after a restart the rotated pair must still work: %v", err)
	}
}

// ---------------------------------------------------------------------------
// T-g: M1 — TrackSessionKey must not revert another process's rotation
// ---------------------------------------------------------------------------

// TrackSessionKey sits on the WebUI hot path (validating or adopting a session
// key via GET /api/session). With saveStoreUnlocked it upserted EVERY client
// in this process's map and DELETEd every DB row missing from it, so a gateway
// whose map was older than the DB reverted the rotations another process had
// just persisted — the expelled client reappearing — and dropped clients it
// had not loaded yet.
func TestTrackSessionKey_DoesNotRevertForeignRotations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	authA, s := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	repo := s.NativeClients()

	rotated, _, rotatedRefreshA := pairTestClient(t, authA, "rotated-device")
	tracked, _, _ := pairTestClient(t, authA, "tracked-device")

	// A client that exists only in the shared DB (another process paired it):
	// a whole-store save from a manager that does not know it would DELETE it.
	foreign := seedForeignClient(t, repo, "foreign-cli")

	// B starts with a private copy of both clients — the gateway side by side
	// with a CLI over one lele.db.
	authB, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	if _, ok := authB.GetClient(rotated.ClientID); !ok {
		t.Fatal("B must see the client A paired")
	}
	if _, ok := authB.GetClient(tracked.ClientID); !ok {
		t.Fatal("B must see the tracked client A paired")
	}

	// A rotates X; B's copy of X is now older than the DB.
	_, _, rotatedRefreshA1, err := authA.RefreshToken(rotatedRefreshA)
	if err != nil {
		t.Fatalf("A: refresh: %v", err)
	}
	beforeRotated := readClientRow(t, repo, rotated.ClientID)
	beforeForeign := readClientRow(t, repo, foreign.ClientID)

	// B tracks a session key on the OTHER client it knows about.
	authB.TrackSessionKey(tracked.ClientID, "webui-session-key")

	// The tracked row is the only one B may touch: X's row must still be
	// exactly what A wrote...
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

	// ...the row of a client B does not know must survive...
	if after := readClientRow(t, repo, foreign.ClientID); after != beforeForeign {
		t.Errorf("TrackSessionKey rewrote/deleted a client it does not know:\n before=%s\n after =%s",
			beforeForeign, after)
	}

	// ...and the tracked row must actually carry the new session key.
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

// ---------------------------------------------------------------------------
// T-h: M2 — the replay grace is single use ACROSS processes
// ---------------------------------------------------------------------------

// Inside one process rotateClientLocked re-arms Prev* so a superseded token
// buys one pair; but the grace was only consumed in that process's memory. A
// second process whose copy still named the old token as PREVIOUS could accept
// it again — the promise in rotationGrace's comment ("single use") is a
// property of the stored row, so the replay now clears Prev* on the row.
func TestRefreshToken_ReplayGraceIsSingleUseAcrossProcesses(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	authA, s := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	repo := s.NativeClients()

	client, _, refresh0 := pairTestClient(t, authA, "shared-grace-device")

	// A rotates once: the row now records refresh0 as the PREVIOUS token.
	if _, _, _, err := authA.RefreshToken(refresh0); err != nil {
		t.Fatalf("A: rotation 1: %v", err)
	}

	// B is a second process with its own map: it sees the rotation's current
	// value and the PREVIOUS hash refresh0, and only looks at the DB again
	// when its own slow path reloads.
	authB, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	if _, ok := authB.GetClient(client.ClientID); !ok {
		t.Fatal("B must see the client A rotated")
	}

	// The client replays refresh0 (its rotation response was lost) to B. B
	// reloads, finds Prev = hash(refresh0) inside the grace and hands out a
	// pair — once.
	replayed, _, refresh2, err := authB.RefreshToken(refresh0)
	if err != nil {
		t.Fatalf("the replay inside the grace must be accepted once, got: %v", err)
	}
	if replayed == nil || replayed.ClientID != client.ClientID {
		t.Fatalf("the replay must return the same client, got %v", replayed)
	}

	// Consuming the grace happens ON THE ROW, which is what makes the
	// superseded token dead for every other process. Before the explicit
	// consume this row still carried Prev = the rotation's previous hash, i.e.
	// replay material any other (stale) process could spend.
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

	// A third process that loads the DB afterwards rejects refresh0: the
	// grace is out of material, not merely re-armed.
	authC, _ := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	_, _, _, err = authC.RefreshToken(refresh0)
	requireInvalidRefresh(t, err, "replay of a consumed grace in a third process")

	// The pair the replay issued is the live one.
	if _, _, _, err := authC.RefreshToken(refresh2); err != nil {
		t.Fatalf("the pair issued by the replay must survive: %v", err)
	}
}

// ---------------------------------------------------------------------------
// T-i: every single-client mutator writes ONLY its own row (SQLite)
// ---------------------------------------------------------------------------

// The M1 defect was not specific to RefreshToken: any mutator that persisted
// with saveStoreUnlocked rewrote the whole table from this process's map and
// DELETEd rows it did not know about. Each step below leaves a "foreign" row
// (present in the DB, absent from this manager's map) in place and checks it
// survives the mutation.
func TestSingleClientMutators_DoNotTouchForeignRows(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	auth, s := openRotationAuthManagerSQLite(t, tmpDir, dbPath)
	repo := s.NativeClients()

	sessionClient, _, _ := pairTestClient(t, auth, "session-device")
	doomed, _, _ := pairTestClient(t, auth, "revoked-device")
	foreign := seedForeignClient(t, repo, "foreign-cli")
	beforeForeign := readClientRow(t, repo, foreign.ClientID)

	// Every mutator that should be a directed write, in order. A whole-store
	// save would delete the foreign row at the first one.
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

	// The mutations did land: the tracked key came and went, the desktop
	// client exists, the revoked row is gone and the late pairing was stored.
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
	// Exactly the four rows that should exist: the foreign one, the session
	// client, the desktop client and the client paired last. In particular the
	// late pairing added a row WITHOUT dropping the foreign one.
	if n, err := repo.CountClients(); err != nil {
		t.Fatalf("CountClients: %v", err)
	} else if n != 4 {
		t.Errorf("client row count = %d, want 4 (foreign, session, desktop, late)", n)
	}
}
