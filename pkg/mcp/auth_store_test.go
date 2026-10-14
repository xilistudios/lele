package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
)

// These tests are the T4 contract: the file format and its 0600 mode, the URL
// binding, the "usable token" predicate, the revision that invalidates a
// cached connection, the disabled store, Clear's file removal and the
// cross-process stat revalidation. Nothing here touches the network and every
// store is built on t.TempDir(), so the tests never read or write a real
// lele directory.

// authTestURL / authTestOtherURL are the two resource URLs the URL-binding
// tests swap: a record saved for one must be absent for the other.
const (
	authTestURL      = "https://mcp.example.com/mcp"
	authTestOtherURL = "https://mcp.example.com/other"
)

// newAuthTestStore returns a store on a fresh temp dir plus the path of the
// file it owns.
func newAuthTestStore(t *testing.T) (*AuthStore, string) {
	t.Helper()
	dir := t.TempDir()
	return NewAuthStore(dir), filepath.Join(dir, authFileName)
}

// authTestRecord is one server's record with every field populated, so a test
// can tell "preserved" from "silently dropped".
func authTestRecord(t *testing.T, url, access string) AuthRecord {
	t.Helper()
	return AuthRecord{
		URL:                 url,
		ClientID:            "client-id",
		ClientSecret:        "client-secret",
		ResourceMetadataURL: "https://mcp.example.com/.well-known/oauth-protected-resource",
		RedirectURI:         "http://127.0.0.1:8123/callback",
		AuthRequired:        true,
		Token: &transport.Token{
			AccessToken:  access,
			TokenType:    "Bearer",
			RefreshToken: "refresh-token",
			ExpiresIn:    3600,
			Scope:        "mcp.read",
			ExpiresAt:    time.Now().Add(time.Hour),
		},
	}
}

// assertAuthTestToken compares tokens field by field: reflect.DeepEqual would
// compare time.Time's monotonic component and location pointer, which says
// nothing about the stored state.
func assertAuthTestToken(t *testing.T, got, want *transport.Token) {
	t.Helper()
	if (got == nil) != (want == nil) {
		t.Fatalf("token presence = %v, want %v", got != nil, want != nil)
	}
	if got == nil {
		return
	}
	if got.AccessToken != want.AccessToken ||
		got.TokenType != want.TokenType ||
		got.RefreshToken != want.RefreshToken ||
		got.ExpiresIn != want.ExpiresIn ||
		got.Scope != want.Scope {
		t.Fatalf("token = %+v, want %+v", *got, *want)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("token ExpiresAt = %v, want %v", got.ExpiresAt, want.ExpiresAt)
	}
}

// assertAuthTestRecord compares every field of a record, token included.
func assertAuthTestRecord(t *testing.T, got, want AuthRecord) {
	t.Helper()
	if got.URL != want.URL ||
		got.ClientID != want.ClientID ||
		got.ClientSecret != want.ClientSecret ||
		got.ResourceMetadataURL != want.ResourceMetadataURL ||
		got.RedirectURI != want.RedirectURI ||
		got.AuthRequired != want.AuthRequired {
		t.Fatalf("record = %+v, want %+v", got, want)
	}
	assertAuthTestToken(t, got.Token, want.Token)
}

// TestAuthStoreSaveRecordRoundTrip proves the write/read pair and the on-disk
// format: the envelope is {"servers":{...}} keyed by name, the field names are
// the documented JSON ones, and the file is 0600 because it holds secrets.
func TestAuthStoreSaveRecordRoundTrip(t *testing.T) {
	store, path := newAuthTestStore(t)
	rec := authTestRecord(t, authTestURL, "access-1")

	if err := store.Save("srv", rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok := store.Record("srv")
	if !ok {
		t.Fatal("Record after Save: ok = false, want true")
	}
	assertAuthTestRecord(t, got, rec)

	if !store.Authenticated("srv", authTestURL) {
		t.Fatal("Authenticated = false, want true for a stored non-expired token")
	}
	if !store.AuthRequired("srv", authTestURL) {
		t.Fatal("AuthRequired = false, want true (the record carries the flag)")
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Fatalf("file mode = %o, want 0600 (tokens are secrets)", perm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var raw struct {
		Servers map[string]map[string]json.RawMessage `json:"servers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("on-disk file is not the documented envelope: %v", err)
	}
	entry, ok := raw.Servers["srv"]
	if !ok {
		t.Fatalf("on-disk servers keys = %v, want one \"srv\" entry", raw.Servers)
	}
	for _, key := range []string{"url", "client_id", "client_secret", "resource_metadata_url", "redirect_uri", "auth_required", "token"} {
		if _, ok := entry[key]; !ok {
			t.Errorf("on-disk record is missing JSON key %q", key)
		}
	}

	// A second, unrelated name must not disturb the first record.
	if err := store.Save("srv-2", authTestRecord(t, authTestOtherURL, "access-2")); err != nil {
		t.Fatalf("Save srv-2: %v", err)
	}
	got, ok = store.Record("srv")
	if !ok {
		t.Fatal("Record srv after saving srv-2: ok = false, want true")
	}
	assertAuthTestRecord(t, got, rec)
}

// TestAuthStoreURLBinding is the URL-binding table: a record is bound to the
// resource URL it was obtained for, so any read for another URL sees nothing.
func TestAuthStoreURLBinding(t *testing.T) {
	cases := []struct {
		name     string
		record   string // URL stored in the record
		query    string // URL the caller asks about
		wantSeen bool
	}{
		{name: "matching URL", record: authTestURL, query: authTestURL, wantSeen: true},
		{name: "different path", record: authTestURL, query: authTestOtherURL, wantSeen: false},
		{name: "different scheme", record: authTestURL, query: "http://mcp.example.com/mcp", wantSeen: false},
		{name: "query string added", record: authTestURL, query: authTestURL + "?v=2", wantSeen: false},
		{name: "empty record URL", record: "", query: authTestURL, wantSeen: false},
		{name: "empty query URL", record: authTestURL, query: "", wantSeen: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newAuthTestStore(t)
			// Record is the RAW accessor: it sees the record whatever its URL.
			if err := store.Save("srv", authTestRecord(t, tc.record, "access-1")); err != nil {
				t.Fatalf("Save: %v", err)
			}
			if _, ok := store.Record("srv"); !ok {
				t.Fatal("Record = absent, want the raw record regardless of URL")
			}

			if got := store.Authenticated("srv", tc.query); got != tc.wantSeen {
				t.Errorf("Authenticated = %v, want %v", got, tc.wantSeen)
			}
			if got := store.AuthRequired("srv", tc.query); got != tc.wantSeen {
				t.Errorf("AuthRequired = %v, want %v", got, tc.wantSeen)
			}

			_, err := store.TokenStore("srv", tc.query).GetToken(context.Background())
			if tc.wantSeen && err != nil {
				t.Errorf("GetToken error = %v, want nil for a matching URL", err)
			}
			if !tc.wantSeen && !errors.Is(err, transport.ErrNoToken) {
				t.Errorf("GetToken error = %v, want transport.ErrNoToken for a URL-mismatched record", err)
			}
		})
	}
}

// TestAuthStoreAbsentRecord pins the "nothing stored" contract every caller
// relies on: no error, no panic, a zero revision and ErrNoToken.
func TestAuthStoreAbsentRecord(t *testing.T) {
	store, path := newAuthTestStore(t)

	if _, ok := store.Record("missing"); ok {
		t.Fatal("Record = present on an empty store, want absent")
	}
	if got := store.Revision("missing"); got != 0 {
		t.Fatalf("Revision = %d, want 0 when absent", got)
	}
	if store.Authenticated("missing", authTestURL) {
		t.Fatal("Authenticated = true with no record, want false")
	}
	if store.AuthRequired("missing", authTestURL) {
		t.Fatal("AuthRequired = true with no record, want false")
	}
	if _, err := store.TokenStore("missing", authTestURL).GetToken(context.Background()); !errors.Is(err, transport.ErrNoToken) {
		t.Fatalf("GetToken error = %v, want transport.ErrNoToken", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a read created %s (stat error = %v)", path, err)
	}

	// An absent record whose Revision is asked for twice must stay 0 (the
	// cached "file does not exist" load must not turn into a parse attempt).
	if got := store.Revision("missing"); got != 0 {
		t.Fatalf("second Revision = %d, want 0", got)
	}
}

// TestAuthStoreAuthenticatedUsability is the predicate table. `usable` is what
// Authenticated must answer; `handedOut` is whether GetToken must return the
// token at all — GetToken deliberately hands over an EXPIRED token (that is how
// mcp-go gets a refresh token to work with), so the two columns differ for an
// expired token without a refresh token.
func TestAuthStoreAuthenticatedUsability(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	cases := []struct {
		name      string
		token     *transport.Token
		usable    bool
		handedOut bool
	}{
		{
			name:      "non-expired token with refresh token",
			token:     &transport.Token{AccessToken: "at", RefreshToken: "rt", ExpiresAt: future},
			usable:    true,
			handedOut: true,
		},
		{
			name:      "non-expired token without refresh token",
			token:     &transport.Token{AccessToken: "at", ExpiresAt: future},
			usable:    true,
			handedOut: true,
		},
		{
			name:      "expired token with refresh token",
			token:     &transport.Token{AccessToken: "at", RefreshToken: "rt", ExpiresAt: past},
			usable:    true,
			handedOut: true,
		},
		{
			name:      "expired token without refresh token",
			token:     &transport.Token{AccessToken: "at", ExpiresAt: past},
			usable:    false,
			handedOut: true,
		},
		{
			name:      "no expiry known",
			token:     &transport.Token{AccessToken: "at"},
			usable:    true,
			handedOut: true,
		},
		{
			name:      "refresh token only, not expired",
			token:     &transport.Token{RefreshToken: "rt", ExpiresAt: future},
			usable:    false,
			handedOut: false,
		},
		{
			name:      "refresh token only, expired",
			token:     &transport.Token{RefreshToken: "rt", ExpiresAt: past},
			usable:    false,
			handedOut: false,
		},
		{
			name:      "no token at all",
			token:     nil,
			usable:    false,
			handedOut: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newAuthTestStore(t)
			if err := store.Save("srv", AuthRecord{URL: authTestURL, Token: tc.token}); err != nil {
				t.Fatalf("Save: %v", err)
			}
			if got := store.Authenticated("srv", authTestURL); got != tc.usable {
				t.Fatalf("Authenticated = %v, want %v", got, tc.usable)
			}

			_, err := store.TokenStore("srv", authTestURL).GetToken(context.Background())
			if handedOut := err == nil; handedOut != tc.handedOut {
				t.Fatalf("GetToken handed out a token = %v (err = %v), want %v", handedOut, err, tc.handedOut)
			}
			if !tc.handedOut && !errors.Is(err, transport.ErrNoToken) {
				t.Fatalf("GetToken error = %v, want transport.ErrNoToken", err)
			}
		})
	}
}

// TestAuthStoreSaveTokenPreservesRegistration proves the read-modify-write:
// transport.Token carries no client_id/client_secret/resource metadata, so a
// naive write would drop the registration and force a fresh DCR on every
// refresh.
func TestAuthStoreSaveTokenPreservesRegistration(t *testing.T) {
	firstTok := transport.Token{AccessToken: "access-1", TokenType: "Bearer", RefreshToken: "refresh-1", ExpiresAt: time.Now().Add(time.Hour)}
	secondTok := transport.Token{AccessToken: "access-2-longer", TokenType: "Bearer", RefreshToken: "refresh-2", ExpiresAt: time.Now().Add(2 * time.Hour)}
	thirdTok := transport.Token{AccessToken: "a3", TokenType: "Bearer", RefreshToken: "refresh-3", ExpiresAt: time.Now().Add(3 * time.Hour)}

	store, path := newAuthTestStore(t)
	registration := authTestRecord(t, authTestURL, firstTok.AccessToken)
	registration.Token = &firstTok
	if err := store.Save("srv", registration); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ts := store.TokenStore("srv", authTestURL)
	if err := ts.SaveToken(context.Background(), &secondTok); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	got, ok := store.Record("srv")
	if !ok {
		t.Fatal("Record after SaveToken: ok = false, want true")
	}
	want := registration
	want.Token = &secondTok
	assertAuthTestRecord(t, got, want)

	// The registration must also have survived to disk, not just to the cache.
	if err := ts.SaveToken(context.Background(), &thirdTok); err != nil {
		t.Fatalf("second SaveToken: %v", err)
	}
	reloaded := NewAuthStore(filepath.Dir(path))
	got, ok = reloaded.Record("srv")
	if !ok {
		t.Fatal("Record from a second store: ok = false, want true")
	}
	want.Token = &thirdTok
	assertAuthTestRecord(t, got, want)

	// A stale URL on the record is corrected by SaveToken: the adapter is
	// bound to the URL the manager dialled.
	stale := registration
	stale.URL = authTestOtherURL
	if err := store.Save("srv-stale", stale); err != nil {
		t.Fatalf("Save stale: %v", err)
	}
	if err := store.TokenStore("srv-stale", authTestURL).SaveToken(context.Background(), &thirdTok); err != nil {
		t.Fatalf("SaveToken stale: %v", err)
	}
	got, ok = store.Record("srv-stale")
	if !ok {
		t.Fatal("Record srv-stale: ok = false, want true")
	}
	if got.URL != authTestURL {
		t.Fatalf("URL = %q after SaveToken, want %q", got.URL, authTestURL)
	}
	if !store.Authenticated("srv-stale", authTestURL) {
		t.Fatal("Authenticated = false after SaveToken rebound the record, want true")
	}
}

// TestAuthStoreGetTokenReturnsCopy guards the cache against the transport
// mutating a token it was handed.
func TestAuthStoreGetTokenReturnsCopy(t *testing.T) {
	store, _ := newAuthTestStore(t)
	rec := authTestRecord(t, authTestURL, "access-1")
	if err := store.Save("srv", rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ts := store.TokenStore("srv", authTestURL)
	first, err := ts.GetToken(context.Background())
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	first.AccessToken = "tampered"
	first.RefreshToken = "tampered"

	second, err := ts.GetToken(context.Background())
	if err != nil {
		t.Fatalf("second GetToken: %v", err)
	}
	if second.AccessToken != "access-1" || second.RefreshToken != "refresh-token" {
		t.Fatalf("stored token was mutated through the returned pointer: %+v", *second)
	}
	if got, _ := store.Record("srv"); got.Token.AccessToken != "access-1" {
		t.Fatalf("Record token = %q, want access-1", got.Token.AccessToken)
	}
}

// TestAuthStoreTokenStoreContext checks the interface contract: a cancelled or
// expired context wins over any stored token.
func TestAuthStoreTokenStoreContext(t *testing.T) {
	store, _ := newAuthTestStore(t)
	if err := store.Save("srv", authTestRecord(t, authTestURL, "access-1")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ts := store.TokenStore("srv", authTestURL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ts.GetToken(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetToken with a cancelled ctx = %v, want context.Canceled", err)
	}
	if err := ts.SaveToken(ctx, &transport.Token{AccessToken: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SaveToken with a cancelled ctx = %v, want context.Canceled", err)
	}

	// A disabled store must still honour the context (err, not ErrNoToken).
	disabled := NewAuthStore("")
	if _, err := disabled.TokenStore("srv", authTestURL).GetToken(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("disabled GetToken with a cancelled ctx = %v, want context.Canceled", err)
	}
}

// TestAuthStoreRevision pins the invalidation signal: 0 when absent, changing
// after a write (any field, token included) and stable across reads.
func TestAuthStoreRevision(t *testing.T) {
	store, _ := newAuthTestStore(t)

	if got := store.Revision("srv"); got != 0 {
		t.Fatalf("Revision on an empty store = %d, want 0", got)
	}

	rec := authTestRecord(t, authTestURL, "access-1")
	if err := store.Save("srv", rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	saved := store.Revision("srv")
	if saved == 0 {
		t.Fatal("Revision after Save = 0, want a non-zero hash")
	}
	for i := 0; i < 3; i++ {
		if got := store.Revision("srv"); got != saved {
			t.Fatalf("read %d: Revision = %d, want %d (stable across reads with no write)", i, got, saved)
		}
		if _, ok := store.Record("srv"); !ok {
			t.Fatal("Record: ok = false on a stored record")
		}
	}

	// A different token ⇒ a different revision, even though every other field
	// is identical.
	if err := store.Save("srv", authTestRecord(t, authTestURL, "access-2")); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if got := store.Revision("srv"); got == saved {
		t.Fatalf("Revision = %d after a token change, want a different value than %d", got, saved)
	}
	tokenChanged := store.Revision("srv")

	// A change that only touches a registration field counts too.
	rec.ClientID = "client-id-2"
	if err := store.Save("srv", rec); err != nil {
		t.Fatalf("third Save: %v", err)
	}
	if got := store.Revision("srv"); got == tokenChanged {
		t.Fatalf("Revision = %d after a client_id change, want a different value than %d", got, tokenChanged)
	}

	// The revision is a hash of the RECORD, so it is reproducible: a fresh
	// store (another process) must compute the same value for the same bytes.
	other := NewAuthStore(filepath.Dir(store.path))
	if got, want := other.Revision("srv"), store.Revision("srv"); got != want {
		t.Fatalf("Revision from a second store = %d, want %d (same bytes)", got, want)
	}

	if err := store.Clear("srv"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if got := store.Revision("srv"); got != 0 {
		t.Fatalf("Revision after Clear = %d, want 0", got)
	}
}

// TestAuthStoreDisabledIsNoop pins NewAuthStore(""): every read is "absent",
// every write is a silent no-op and — the important part — no process-relative
// mcp-auth.json appears anywhere.
func TestAuthStoreDisabledIsNoop(t *testing.T) {
	store := NewAuthStore("")
	if store.path != "" {
		t.Fatalf("disabled store path = %q, want empty", store.path)
	}

	ctx := context.Background()
	if _, ok := store.Record("srv"); ok {
		t.Fatal("Record = present on a disabled store, want absent")
	}
	if got := store.Revision("srv"); got != 0 {
		t.Fatalf("Revision = %d on a disabled store, want 0", got)
	}
	if store.Authenticated("srv", authTestURL) {
		t.Fatal("Authenticated = true on a disabled store, want false")
	}
	if store.AuthRequired("srv", authTestURL) {
		t.Fatal("AuthRequired = true on a disabled store, want false")
	}
	if err := store.Save("srv", authTestRecord(t, authTestURL, "access-1")); err != nil {
		t.Fatalf("Save on a disabled store = %v, want nil", err)
	}
	if err := store.Clear("srv"); err != nil {
		t.Fatalf("Clear on a disabled store = %v, want nil", err)
	}

	ts := store.TokenStore("srv", authTestURL)
	if err := ts.SaveToken(ctx, &transport.Token{AccessToken: "access-1"}); err != nil {
		t.Fatalf("SaveToken on a disabled store = %v, want nil", err)
	}
	if _, err := ts.GetToken(ctx); !errors.Is(err, transport.ErrNoToken) {
		t.Fatalf("disabled GetToken = %v, want transport.ErrNoToken", err)
	}

	// Nothing may be created relative to the process working directory: that
	// is exactly the "never create a process-relative file" rule.
	if _, err := os.Stat(authFileName); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a disabled store created %s in the working directory (stat error = %v)", authFileName, err)
	}
}

// TestAuthStoreClear walks the deletion rules: the record goes, the other
// records stay, the flag goes with it and the file disappears when the last
// record does.
func TestAuthStoreClear(t *testing.T) {
	store, path := newAuthTestStore(t)

	// Clearing something that was never stored must not create a file.
	if err := store.Clear("absent"); err != nil {
		t.Fatalf("Clear(absent) = %v, want nil", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Clear(absent) created %s", path)
	}

	if err := store.Save("srv-a", authTestRecord(t, authTestURL, "access-a")); err != nil {
		t.Fatalf("Save srv-a: %v", err)
	}
	if err := store.Save("srv-b", authTestRecord(t, authTestOtherURL, "access-b")); err != nil {
		t.Fatalf("Save srv-b: %v", err)
	}

	if err := store.Clear("srv-a"); err != nil {
		t.Fatalf("Clear srv-a: %v", err)
	}
	if _, ok := store.Record("srv-a"); ok {
		t.Fatal("Record srv-a after Clear: ok = true, want false")
	}
	if store.Authenticated("srv-a", authTestURL) {
		t.Fatal("Authenticated srv-a after Clear = true, want false")
	}
	if store.AuthRequired("srv-a", authTestURL) {
		t.Fatal("AuthRequired srv-a after Clear = true, want false")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the file must survive while srv-b is stored: %v", err)
	}
	if _, ok := store.Record("srv-b"); !ok {
		t.Fatal("Record srv-b after clearing srv-a: ok = false, want true")
	}

	if err := store.Clear("srv-b"); err != nil {
		t.Fatalf("Clear srv-b: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("clearing the last record left %s (stat error = %v)", path, err)
	}
	if _, ok := store.Record("srv-b"); ok {
		t.Fatal("Record srv-b after Clear: ok = true, want false")
	}

	// A second Clear of the same name is a no-op, not an error.
	if err := store.Clear("srv-b"); err != nil {
		t.Fatalf("second Clear(srv-b) = %v, want nil", err)
	}
}

// TestAuthStoreCrossProcessRefresh is the stat-revalidation test: the file is
// written DIRECTLY with SaveRawFile (as the other lele process would) while
// this store's cache is warm, and the change must be visible immediately.
func TestAuthStoreCrossProcessRefresh(t *testing.T) {
	t.Run("rewritten by another process", func(t *testing.T) {
		store, path := newAuthTestStore(t)
		if err := store.Save("srv", authTestRecord(t, authTestURL, "access-1")); err != nil {
			t.Fatalf("Save: %v", err)
		}
		warm := store.Revision("srv")
		if warm == 0 {
			t.Fatal("Revision = 0 after Save")
		}

		// The other process writes a record with a fresh token AND a new
		// field, through the same atomic helper, without this store's help.
		other := authTestRecord(t, authTestURL, "access-from-the-TUI")
		other.RedirectURI = "http://127.0.0.1:9999/other-callback"
		doc, err := json.Marshal(authEnvelope{Servers: map[string]AuthRecord{"srv": other}})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := SaveRawFile(path, doc); err != nil {
			t.Fatalf("SaveRawFile: %v", err)
		}

		got, ok := store.Record("srv")
		if !ok {
			t.Fatal("Record after an outside write: ok = false, want true")
		}
		assertAuthTestRecord(t, got, other)
		if tok, err := store.TokenStore("srv", authTestURL).GetToken(context.Background()); err != nil {
			t.Fatalf("GetToken after an outside write: %v", err)
		} else if tok.AccessToken != "access-from-the-TUI" {
			t.Fatalf("GetToken access token = %q, want the value written by the other process", tok.AccessToken)
		}
		if got := store.Revision("srv"); got == warm {
			t.Fatalf("Revision = %d, want it to change after the outside write", got)
		}
	})

	t.Run("created by another process", func(t *testing.T) {
		store, path := newAuthTestStore(t)
		// Warm the cache with a "file does not exist" load.
		if _, ok := store.Record("srv"); ok {
			t.Fatal("Record on an empty store: ok = true, want false")
		}

		rec := authTestRecord(t, authTestURL, "access-late")
		doc, err := json.Marshal(authEnvelope{Servers: map[string]AuthRecord{"srv": rec}})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := SaveRawFile(path, doc); err != nil {
			t.Fatalf("SaveRawFile: %v", err)
		}

		got, ok := store.Record("srv")
		if !ok {
			t.Fatal("Record after the file appeared: ok = false, want true")
		}
		assertAuthTestRecord(t, got, rec)
	})

	t.Run("removed by another process", func(t *testing.T) {
		store, path := newAuthTestStore(t)
		if err := store.Save("srv", authTestRecord(t, authTestURL, "access-1")); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if !store.Authenticated("srv", authTestURL) {
			t.Fatal("Authenticated = false after Save, want true")
		}

		if err := os.Remove(path); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, ok := store.Record("srv"); ok {
			t.Fatal("Record after the file disappeared: ok = true, want false")
		}
		if store.Authenticated("srv", authTestURL) {
			t.Fatal("Authenticated = true after the file disappeared, want false")
		}
		if got := store.Revision("srv"); got != 0 {
			t.Fatalf("Revision = %d after the file disappeared, want 0", got)
		}
	})
}

// TestAuthStoreMalformedFileIsEmptyThenRepaired documents the deliberate
// choice: a file we cannot parse reads as empty (no error, no panic) and the
// next Save rewrites it from that empty map.
func TestAuthStoreMalformedFileIsEmptyThenRepaired(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{name: "truncated JSON", content: `{"servers":{"srv":{"url":"https://x`},
		{name: "not an object", content: `[]`},
		{name: "empty file", content: ``},
		{name: "wrong record type", content: `{"servers":{"srv":"nope"}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, authFileName)
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			store := NewAuthStore(dir)

			if _, ok := store.Record("srv"); ok {
				t.Fatal("Record = present for a malformed file, want absent")
			}
			if got := store.Revision("srv"); got != 0 {
				t.Fatalf("Revision = %d for a malformed file, want 0", got)
			}

			rec := authTestRecord(t, authTestURL, "access-repair")
			if err := store.Save("srv", rec); err != nil {
				t.Fatalf("Save over a malformed file: %v", err)
			}
			got, ok := store.Record("srv")
			if !ok {
				t.Fatal("Record after the repairing Save: ok = false, want true")
			}
			assertAuthTestRecord(t, got, rec)

			// The repaired file must be readable by a fresh store too.
			if _, ok := NewAuthStore(dir).Record("srv"); !ok {
				t.Fatal("a fresh store cannot read the repaired file")
			}
		})
	}
}

// TestAuthStoreMissingLeleDirIsCreated proves SaveRawFile's parent-directory
// creation is what makes a fresh install work: the directory need not exist.
func TestAuthStoreMissingLeleDirIsCreated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does", "not", "exist")
	store := NewAuthStore(dir)

	if err := store.Save("srv", authTestRecord(t, authTestURL, "access-1")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path := filepath.Join(dir, authFileName)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Fatalf("file mode = %o, want 0600", perm)
	}
	if !store.Authenticated("srv", authTestURL) {
		t.Fatal("Authenticated = false after Save, want true")
	}
}

// TestAuthStoreConcurrentSaveAndRecord is the race-detector hammer: writers
// and readers on one store (the shared-goroutine reality: a refresh from a
// transport goroutine while the manager reads revisions).
func TestAuthStoreConcurrentSaveAndRecord(t *testing.T) {
	store, path := newAuthTestStore(t)
	const (
		writers = 4
		readers = 4
		iters   = 25
	)
	urls := []string{authTestURL, authTestOtherURL}

	var wg sync.WaitGroup
	wg.Add(writers + readers)
	for w := 0; w < writers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				name := fmt.Sprintf("srv-%d", i%2)
				rec := AuthRecord{
					URL:          urls[(w+i)%len(urls)],
					ClientID:     fmt.Sprintf("client-%d", w),
					AuthRequired: true,
					Token:        &transport.Token{AccessToken: fmt.Sprintf("access-%d-%d", w, i), ExpiresAt: time.Now().Add(time.Hour)},
				}
				if err := store.Save(name, rec); err != nil {
					t.Errorf("concurrent Save: %v", err)
					return
				}
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		go func(r int) {
			defer wg.Done()
			ctx := context.Background()
			for i := 0; i < iters; i++ {
				_, _ = store.Record("srv-0")
				_ = store.Revision("srv-1")
				_ = store.Authenticated("srv-0", authTestURL)
				_ = store.AuthRequired("srv-1", authTestOtherURL)
				ts := store.TokenStore("srv-0", authTestURL)
				if _, err := ts.GetToken(ctx); err != nil && !errors.Is(err, transport.ErrNoToken) {
					t.Errorf("concurrent GetToken: %v", err)
					return
				}
				if err := ts.SaveToken(ctx, &transport.Token{AccessToken: fmt.Sprintf("reader-%d-%d", r, i)}); err != nil {
					t.Errorf("concurrent SaveToken: %v", err)
					return
				}
			}
		}(r)
	}
	wg.Wait()

	// Whatever order the writes landed in, the file must still be the
	// documented envelope and readable by another process.
	other := NewAuthStore(filepath.Dir(path))
	if _, ok := other.Record("srv-0"); !ok {
		t.Fatal("srv-0 missing after the hammer: the last writer must win, not lose data")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat after the hammer: %v", err)
	}
}

// TestAuthTokenStoreSaveTokenDoesNotAliasTheCallersToken probes the finding-F4
// claim that SaveToken stores the CALLER's *transport.Token (auth_store.go:
// `rec.Token = tok`) and that mutating it afterwards rewrites the store.
//
// THE PROBE CAME OUT NEGATIVE, and the reason is worth recording: the record
// handed to Save is put into a TEMPORARY map (Save copies s.servers, sets the
// new record in the copy and hands that map to writeLocked), so the caller's
// pointer never reaches the cache; a successful write then calls
// invalidateLocked, so the next read reloads the record from disk (fresh
// tokens), and a failed write leaves the cache exactly as it was. No reader —
// GetToken, Record, Revision or a second AuthStore on the same directory — can
// observe the caller's mutation, which is what this test asserts.
//
// It is kept as the guard for that observable contract: a change that made the
// cache RETAIN the written record (the obvious optimisation: assigning
// s.servers = servers instead of invalidating) would make the aliasing real, and
// this test is what would catch it. The copy in Save/GetToken/Record is what
// pkg/mcp documents as the store's ownership rule; see copyRecord.
func TestAuthTokenStoreSaveTokenDoesNotAliasTheCallersToken(t *testing.T) {
	store, path := newAuthTestStore(t)
	ctx := context.Background()
	ts := store.TokenStore("srv", authTestURL)

	tok := &transport.Token{
		AccessToken:  "access-token",
		TokenType:    "Bearer",
		RefreshToken: "refresh-token",
		ExpiresIn:    3600,
		Scope:        "mcp.read",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	if err := ts.SaveToken(ctx, tok); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	before := store.Revision("srv")

	// The caller keeps its object and mutates it afterwards. transport.Token is
	// strings, one int64 and a time.Time — there is no slice, map or pointer
	// inside it, so a struct copy IS a deep copy and nothing here can be left
	// aliased by accident.
	tok.AccessToken = "mutated-access-token"
	tok.RefreshToken = ""
	tok.TokenType = "MAC"
	tok.Scope = "mutated.scope"
	tok.ExpiresIn = 0
	tok.ExpiresAt = time.Time{}

	got, err := ts.GetToken(ctx)
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if got.AccessToken != "access-token" || got.RefreshToken != "refresh-token" ||
		got.TokenType != "Bearer" || got.Scope != "mcp.read" || got.ExpiresIn != 3600 || got.ExpiresAt.IsZero() {
		t.Fatalf("GetToken = %s after the caller mutated its token, want the values as they were saved", redactedToken(got))
	}

	// The record the cache serves is a copy as well, and the revision (the
	// hash of the stored bytes) did not move.
	rec, ok := store.Record("srv")
	if !ok {
		t.Fatal("record missing after SaveToken")
	}
	if rec.Token == nil || rec.Token.AccessToken != "access-token" || rec.Token.RefreshToken != "refresh-token" {
		t.Fatalf("Record = %s, want the values as they were saved", redactedRecord(rec))
	}
	if after := store.Revision("srv"); after != before {
		t.Fatalf("Revision changed after the caller mutated its token (%d → %d)", before, after)
	}

	// And so is what another process reads back from the file.
	other := NewAuthStore(filepath.Dir(path))
	if !other.Authenticated("srv", authTestURL) {
		t.Fatal("the stored token is not usable for another reader")
	}
	if raw, err := os.ReadFile(path); err != nil {
		t.Fatalf("read %s: %v", path, err)
	} else if strings.Contains(string(raw), "mutated") {
		t.Fatalf("the caller's mutation reached the file: %s", raw)
	}
}
