package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/xilistudios/lele/pkg/mcp/authtest"
)

type fakeVault struct {
	mu      sync.Mutex
	secrets map[string]string

	getErr    error
	setErr    error
	delErr    error
	setErrFor map[string]error

	getCalls int
	setCalls int
	delCalls int
}

func newFakeVault() *fakeVault {
	return &fakeVault{secrets: map[string]string{}, setErrFor: map[string]error{}}
}

func (v *fakeVault) GetSecret(name string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.getCalls++
	if v.getErr != nil {
		return "", v.getErr
	}
	return v.secrets[name], nil
}

func (v *fakeVault) SetSecret(name, value string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.setCalls++
	if err, ok := v.setErrFor[name]; ok {
		return err
	}
	if v.setErr != nil {
		return v.setErr
	}
	v.secrets[name] = value
	return nil
}

func (v *fakeVault) DeleteSecret(name string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.delCalls++
	if v.delErr != nil {
		return v.delErr
	}
	delete(v.secrets, name)
	return nil
}

func (v *fakeVault) stored(name string) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	raw, ok := v.secrets[name]
	return raw, ok
}

func (v *fakeVault) blob(t *testing.T, server string) authSecretBlob {
	t.Helper()
	raw, ok := v.stored(secretNameFor(server))
	if !ok {
		t.Fatalf("no vault entry for server %q (expected %q)", server, secretNameFor(server))
	}
	var blob authSecretBlob
	if err := json.Unmarshal([]byte(raw), &blob); err != nil {
		t.Fatalf("vault payload for %q is not valid JSON: %v (%q)", server, err, raw)
	}
	return blob
}

func (v *fakeVault) names() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]string, 0, len(v.secrets))
	for name := range v.secrets {
		out = append(out, name)
	}
	return out
}

func (v *fakeVault) counts() (get, set, del int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.getCalls, v.setCalls, v.delCalls
}

func (v *fakeVault) setFailFor(server string, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.setErrFor[secretNameFor(server)] = err
}

func (v *fakeVault) setFailEvery(err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.setErr = err
}

func (v *fakeVault) setGetErr(err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.getErr = err
}

func (v *fakeVault) setDelErr(err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.delErr = err
}

func (v *fakeVault) seed(t *testing.T, server string, blob authSecretBlob) {
	t.Helper()
	payload, err := encodeAuthSecretBlob(blob)
	if err != nil {
		t.Fatalf("encode seed blob: %v", err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.secrets[secretNameFor(server)] = payload
}

func newVaultTestStore(t *testing.T) (*AuthStore, *fakeVault, string) {
	t.Helper()
	dir := t.TempDir()
	vault := newFakeVault()
	return NewAuthStore(dir, WithSecretVault(vault)), vault, filepath.Join(dir, authFileName)
}

func writeLegacyAuthFile(t *testing.T, dir string, servers map[string]AuthRecord) string {
	t.Helper()
	content, err := json.Marshal(authEnvelope{Servers: servers})
	if err != nil {
		t.Fatalf("marshal legacy auth file: %v", err)
	}
	path := filepath.Join(dir, authFileName)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("write legacy auth file: %v", err)
	}
	return path
}

func readAuthFileRecords(t *testing.T, path string) map[string]AuthRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var env authEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode %s: %v (%s)", path, err, data)
	}
	return env.Servers
}

func assertFileHoldsNoSecrets(t *testing.T, path string, secretValues ...string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, member := range []string{`"client_secret"`, `"token"`, `"access_token"`, `"refresh_token"`} {
		if bytes.Contains(data, []byte(member)) {
			t.Fatalf("mcp-auth.json still holds %s: %s", member, data)
		}
	}
	for _, value := range secretValues {
		if value == "" {
			continue
		}
		if bytes.Contains(data, []byte(value)) {
			t.Fatalf("mcp-auth.json still holds %q: %s", value, data)
		}
	}
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })
	return &buf
}

func resetSecretVaultFactory(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { RegisterSecretVaultFactory(nil) })
}

func TestAuthStoreVaultSaveSplitsSecrets(t *testing.T) {
	store, vault, path := newVaultTestStore(t)
	rec := authTestRecord(t, authTestURL, "access-token")

	if err := store.Save("srv", rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	assertFileHoldsNoSecrets(t, path, "client-secret", "access-token", "refresh-token")
	onDisk := readAuthFileRecords(t, path)["srv"]
	if onDisk.URL != rec.URL || onDisk.ClientID != rec.ClientID {
		t.Fatalf("file record lost non-secret fields: %+v", onDisk)
	}
	if onDisk.ResourceMetadataURL != rec.ResourceMetadataURL || onDisk.RedirectURI != rec.RedirectURI {
		t.Fatalf("file record lost non-secret fields: %+v", onDisk)
	}
	if !onDisk.AuthRequired {
		t.Fatalf("file record lost auth_required: %+v", onDisk)
	}
	if onDisk.ClientSecret != "" || onDisk.Token != nil {
		t.Fatalf("file record still carries secrets: %+v", onDisk)
	}

	blob := vault.blob(t, "srv")
	if blob.ClientSecret != "client-secret" {
		t.Fatalf("vault client_secret = %q, want %q", blob.ClientSecret, "client-secret")
	}
	if blob.Token == nil || blob.Token.AccessToken != "access-token" {
		t.Fatalf("vault token = %+v, want access_token %q", blob.Token, "access-token")
	}
	if blob.Token.RefreshToken != "refresh-token" || blob.Token.TokenType != "Bearer" {
		t.Fatalf("vault token lost fields: %+v", blob.Token)
	}

	got, ok := store.Record("srv")
	if !ok {
		t.Fatal("Record reported no record after Save")
	}
	if got.ClientSecret != rec.ClientSecret {
		t.Fatalf("Record client_secret = %q, want %q", got.ClientSecret, rec.ClientSecret)
	}
	if got.Token == nil || got.Token.AccessToken != rec.Token.AccessToken {
		t.Fatalf("Record token = %+v, want %q", got.Token, rec.Token.AccessToken)
	}
	if got.URL != rec.URL || got.ClientID != rec.ClientID || !got.AuthRequired {
		t.Fatalf("Record lost non-secret fields: %+v", got)
	}
	if !store.Authenticated("srv", authTestURL) {
		t.Fatal("Authenticated = false for a record with a usable token")
	}

	if rev, want := store.Revision("srv"), authRevision(rec); rev != want {
		t.Fatalf("Revision = %d, want %d (the hash of the whole record)", rev, want)
	}
}

func TestAuthStoreVaultTokenStoreRoundTrip(t *testing.T) {
	store, vault, path := newVaultTestStore(t)
	ctx := context.Background()

	if err := store.Save("srv", AuthRecord{
		ClientID:            "client-id",
		ClientSecret:        "client-secret",
		ResourceMetadataURL: "https://mcp.example.com/.well-known/oauth-protected-resource",
		RedirectURI:         "http://127.0.0.1:8123/callback",
		AuthRequired:        true,
	}); err != nil {
		t.Fatalf("Save registration: %v", err)
	}
	if blob := vault.blob(t, "srv"); blob.Token != nil || blob.ClientSecret != "client-secret" {
		t.Fatalf("registration-only blob = %+v, want client_secret only", blob)
	}

	ts := store.TokenStore("srv", authTestURL)
	saved := transport.Token{
		AccessToken:  "access-token",
		TokenType:    "Bearer",
		RefreshToken: "refresh-token",
		ExpiresIn:    3600,
		Scope:        "mcp.read",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	tok := saved
	if err := ts.SaveToken(ctx, &tok); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	assertFileHoldsNoSecrets(t, path, "client-secret", "access-token", "refresh-token")
	onDisk := readAuthFileRecords(t, path)["srv"]
	if onDisk.ClientID != "client-id" || onDisk.RedirectURI != "http://127.0.0.1:8123/callback" || !onDisk.AuthRequired {
		t.Fatalf("SaveToken dropped the registration half: %+v", onDisk)
	}
	if onDisk.URL != authTestURL {
		t.Fatalf("SaveToken did not force the URL: %q, want %q", onDisk.URL, authTestURL)
	}

	blob := vault.blob(t, "srv")
	if blob.ClientSecret != "client-secret" || blob.Token == nil || blob.Token.AccessToken != "access-token" {
		t.Fatalf("vault blob after SaveToken = %+v", blob)
	}
	got, err := ts.GetToken(ctx)
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if got.AccessToken != saved.AccessToken || got.RefreshToken != saved.RefreshToken || got.Scope != saved.Scope {
		t.Fatalf("GetToken = %+v, want %+v", got, saved)
	}

	got.AccessToken = "mutated-by-reader"
	got.RefreshToken = ""
	tok.AccessToken = "mutated-by-caller"
	again, err := ts.GetToken(ctx)
	if err != nil {
		t.Fatalf("GetToken (second): %v", err)
	}
	if again.AccessToken != saved.AccessToken || again.RefreshToken != saved.RefreshToken {
		t.Fatalf("GetToken aliased a caller-owned token: %+v", again)
	}
	rec, ok := store.Record("srv")
	if !ok || rec.Token == nil || rec.Token.AccessToken != saved.AccessToken {
		t.Fatalf("Record token = %+v, want %q", rec.Token, saved.AccessToken)
	}
	rec.Token.AccessToken = "mutated-via-record"
	if third, err := ts.GetToken(ctx); err != nil || third.AccessToken != saved.AccessToken {
		t.Fatalf("Record aliased the cached token: %+v (%v)", third, err)
	}
}

func TestAuthStoreVaultCrossProcessTokenRefresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, authFileName)
	vault := newFakeVault()
	a := NewAuthStore(dir, WithSecretVault(vault))
	b := NewAuthStore(dir, WithSecretVault(vault))
	ctx := context.Background()

	if err := a.Save("srv", AuthRecord{
		URL:          authTestURL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURI:  "http://127.0.0.1:8123/callback",
		AuthRequired: true,
	}); err != nil {
		t.Fatalf("Save registration: %v", err)
	}
	first := &transport.Token{
		AccessToken:  "token-1",
		TokenType:    "Bearer",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	if err := a.TokenStore("srv", authTestURL).SaveToken(ctx, first); err != nil {
		t.Fatalf("SaveToken (first): %v", err)
	}

	gotB, err := b.TokenStore("srv", authTestURL).GetToken(ctx)
	if err != nil || gotB.AccessToken != "token-1" {
		t.Fatalf("B GetToken = %+v, %v; want token-1", gotB, err)
	}
	revBefore := b.Revision("srv")
	bytesBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read auth file: %v", err)
	}
	fiBefore, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat auth file: %v", err)
	}
	mtimeBefore := fiBefore.ModTime()

	second := &transport.Token{
		AccessToken:  "token-2",
		TokenType:    "Bearer",
		RefreshToken: "refresh-2",
		ExpiresAt:    time.Now().Add(2 * time.Hour),
	}
	if err := a.TokenStore("srv", authTestURL).SaveToken(ctx, second); err != nil {
		t.Fatalf("SaveToken (second): %v", err)
	}
	bytesAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read auth file: %v", err)
	}
	if !bytes.Equal(bytesBefore, bytesAfter) {
		t.Fatalf("the file changed on a token refresh:\nbefore=%s\nafter =%s", bytesBefore, bytesAfter)
	}
	if err := os.Chtimes(path, mtimeBefore, mtimeBefore); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	rec, ok := b.Record("srv")
	if !ok {
		t.Fatal("B: no record after the other process refreshed the token")
	}
	if rec.Token == nil || rec.Token.AccessToken != "token-2" {
		t.Fatalf("B: Record token = %+v, want token-2", rec.Token)
	}
	if rec.ClientSecret != "client-secret" || rec.ClientID != "client-id" {
		t.Fatalf("B: Record lost the registration half: %+v", rec)
	}
	if revAfter := b.Revision("srv"); revAfter == revBefore {
		t.Fatalf("B: Revision did not change after a vault-only token refresh (%d)", revAfter)
	}
	gotB, err = b.TokenStore("srv", authTestURL).GetToken(ctx)
	if err != nil {
		t.Fatalf("B GetToken (after refresh): %v", err)
	}
	if gotB.AccessToken != "token-2" || gotB.RefreshToken != "refresh-2" {
		t.Fatalf("B GetToken = %+v, want token-2/refresh-2", gotB)
	}
}

func TestAuthStoreVaultMigratesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	legacy := map[string]AuthRecord{
		"with-secret": authTestRecord(t, authTestURL, "access-token"),
		"plain":       {URL: authTestOtherURL, ClientID: "public-client"},
	}
	path := writeLegacyAuthFile(t, dir, legacy)

	vault := newFakeVault()
	store := NewAuthStore(dir, WithSecretVault(vault))

	blob := vault.blob(t, "with-secret")
	if blob.ClientSecret != "client-secret" || blob.Token == nil || blob.Token.AccessToken != "access-token" {
		t.Fatalf("vault blob after migration = %+v", blob)
	}
	if names := vault.names(); len(names) != 1 || names[0] != secretNameFor("with-secret") {
		t.Fatalf("vault entries = %v, want only %q", names, secretNameFor("with-secret"))
	}

	assertFileHoldsNoSecrets(t, path, "client-secret", "access-token", "refresh-token")
	onDisk := readAuthFileRecords(t, path)
	migrated := onDisk["with-secret"]
	if migrated.URL != legacy["with-secret"].URL || migrated.ClientID != "client-id" ||
		migrated.RedirectURI != legacy["with-secret"].RedirectURI || !migrated.AuthRequired {
		t.Fatalf("migrated file record lost non-secret fields: %+v", migrated)
	}
	if plain := onDisk["plain"]; plain.URL != authTestOtherURL || plain.ClientID != "public-client" {
		t.Fatalf("untouched record changed: %+v", plain)
	}

	got, ok := store.Record("with-secret")
	if !ok || got.ClientSecret != "client-secret" || got.Token == nil || got.Token.AccessToken != "access-token" {
		t.Fatalf("Record after migration = %+v (ok=%v)", got, ok)
	}
	gotPlain, ok := store.Record("plain")
	if !ok || gotPlain.ClientID != "public-client" || gotPlain.ClientSecret != "" || gotPlain.Token != nil {
		t.Fatalf("Record(plain) = %+v (ok=%v)", gotPlain, ok)
	}
	if !store.Authenticated("with-secret", authTestURL) {
		t.Fatal("Authenticated = false after migration")
	}
}

func TestAuthStoreVaultMigrationPartialFailure(t *testing.T) {
	logs := captureLogs(t)
	dir := t.TempDir()
	legacy := map[string]AuthRecord{
		"stuck":  authTestRecord(t, authTestURL, "stuck-access"),
		"moving": authTestRecord(t, authTestOtherURL, "moving-access"),
	}
	path := writeLegacyAuthFile(t, dir, legacy)

	vault := newFakeVault()
	vault.setFailFor("stuck", errors.New("keyring is locked"))

	store := NewAuthStore(dir, WithSecretVault(vault))

	if _, ok := vault.stored(secretNameFor("moving")); !ok {
		t.Fatal("the healthy record was not migrated")
	}
	if _, ok := vault.stored(secretNameFor("stuck")); ok {
		t.Fatal("a record whose SetSecret failed must not appear in the vault")
	}
	onDisk := readAuthFileRecords(t, path)
	if got := onDisk["stuck"]; got.ClientSecret != "client-secret" || got.Token == nil || got.Token.AccessToken != "stuck-access" {
		t.Fatalf("stuck record lost its secrets: %+v", got)
	}
	if got := onDisk["moving"]; got.ClientSecret != "" || got.Token != nil {
		t.Fatalf("migrated record still carries secrets: %+v", got)
	}

	stuck, ok := store.Record("stuck")
	if !ok || stuck.ClientSecret != "client-secret" || stuck.Token == nil || stuck.Token.AccessToken != "stuck-access" {
		t.Fatalf("Record(stuck) = %+v (ok=%v); the unmigrated record must stay readable", stuck, ok)
	}
	moving, ok := store.Record("moving")
	if !ok || moving.ClientSecret != "client-secret" || moving.Token == nil || moving.Token.AccessToken != "moving-access" {
		t.Fatalf("Record(moving) = %+v (ok=%v)", moving, ok)
	}

	vault.seed(t, "stuck", authSecretBlob{
		ClientSecret: "other-vault-secret",
		Token:        &transport.Token{AccessToken: "other-vault-token"},
	})
	stillFile, ok := store.Record("stuck")
	if !ok {
		t.Fatal("Record(stuck) with a conflicting vault blob: absent")
	}
	if stillFile.ClientSecret != "client-secret" || stillFile.Token == nil || stillFile.Token.AccessToken != "stuck-access" {
		t.Fatalf("Record(stuck) = %+v; while the file carries secrets the file must win", stillFile)
	}

	out := logs.String()
	if !strings.Contains(out, "could not be migrated") {
		t.Fatalf("expected a migration warning, got: %q", out)
	}
	for _, secret := range []string{"client-secret", "stuck-access", "moving-access"} {
		if strings.Contains(out, secret) {
			t.Fatalf("the migration warning leaks %q: %s", secret, out)
		}
	}

	vault.clearFailFor("stuck")
	if err := store.Save("stuck", stuck); err != nil {
		t.Fatalf("Save(stuck): %v", err)
	}
	if onDisk := readAuthFileRecords(t, path)["stuck"]; onDisk.ClientSecret != "" || onDisk.Token != nil {
		t.Fatalf("a successful Save must strip the file record: %+v", onDisk)
	}
	vault.seed(t, "stuck", authSecretBlob{ClientSecret: "vault-secret", Token: &transport.Token{AccessToken: "vault-token"}})
	after, ok := store.Record("stuck")
	if !ok {
		t.Fatal("Record(stuck) after Save: absent")
	}
	if after.ClientSecret != "vault-secret" || after.Token == nil || after.Token.AccessToken != "vault-token" {
		t.Fatalf("Record(stuck) = %+v; a migrated record must read from the vault", after)
	}
}

func TestAuthStoreVaultMigrationOverwritesStaleBlob(t *testing.T) {
	dir := t.TempDir()
	legacy := map[string]AuthRecord{"srv": authTestRecord(t, authTestURL, "file-access")}
	path := writeLegacyAuthFile(t, dir, legacy)

	vault := newFakeVault()
	vault.seed(t, "srv", authSecretBlob{
		ClientSecret: "stale-secret",
		Token:        &transport.Token{AccessToken: "stale-token"},
	})

	store := NewAuthStore(dir, WithSecretVault(vault))

	blob := vault.blob(t, "srv")
	if blob.ClientSecret != "client-secret" || blob.Token == nil || blob.Token.AccessToken != "file-access" {
		t.Fatalf("vault blob = %+v, want the file's values to win", blob)
	}
	assertFileHoldsNoSecrets(t, path, "client-secret", "file-access")
	got, ok := store.Record("srv")
	if !ok || got.Token == nil || got.Token.AccessToken != "file-access" {
		t.Fatalf("Record = %+v (ok=%v)", got, ok)
	}
}

func TestAuthStoreVaultMigrationNoFile(t *testing.T) {
	dir := t.TempDir()
	vault := newFakeVault()
	store := NewAuthStore(dir, WithSecretVault(vault))

	if names := vault.names(); len(names) != 0 {
		t.Fatalf("a migration with no file wrote %v", names)
	}
	if _, set, _ := vault.counts(); set != 0 {
		t.Fatalf("SetSecret called %d times with nothing to migrate", set)
	}
	if _, err := os.Stat(filepath.Join(dir, authFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("NewAuthStore created mcp-auth.json: %v", err)
	}
	if _, ok := store.Record("srv"); ok {
		t.Fatal("Record on an empty store reported a record")
	}
}

func TestAuthStoreVaultMigrationMalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, authFileName)
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatalf("write malformed file: %v", err)
	}
	vault := newFakeVault()
	store := NewAuthStore(dir, WithSecretVault(vault))

	if names := vault.names(); len(names) != 0 {
		t.Fatalf("migration of a malformed file wrote %v", names)
	}
	if _, ok := store.Record("srv"); ok {
		t.Fatal("Record on a malformed file reported a record")
	}
}

func TestAuthStoreVaultClear(t *testing.T) {
	store, vault, path := newVaultTestStore(t)
	ctx := context.Background()

	if err := store.Save("one", authTestRecord(t, authTestURL, "one-access")); err != nil {
		t.Fatalf("Save(one): %v", err)
	}
	if err := store.Save("two", authTestRecord(t, authTestOtherURL, "two-access")); err != nil {
		t.Fatalf("Save(two): %v", err)
	}
	if _, ok := vault.stored(secretNameFor("one")); !ok {
		t.Fatal("one: no vault entry after Save")
	}

	if err := store.Clear("one"); err != nil {
		t.Fatalf("Clear(one): %v", err)
	}
	if _, ok := vault.stored(secretNameFor("one")); ok {
		t.Fatal("Clear(one) left the vault entry behind")
	}
	if _, ok := store.Record("one"); ok {
		t.Fatal("Clear(one) left the file record behind")
	}
	if _, ok := vault.stored(secretNameFor("two")); !ok {
		t.Fatal("Clear(one) removed another server's vault entry")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the file must survive while a record remains: %v", err)
	}

	if err := Logout(store, "two"); err != nil {
		t.Fatalf("Logout(two): %v", err)
	}
	if _, ok := vault.stored(secretNameFor("two")); ok {
		t.Fatal("Logout left the vault entry behind")
	}
	if names := vault.names(); len(names) != 0 {
		t.Fatalf("vault entries left after clearing everything: %v", names)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the last Clear must remove mcp-auth.json: %v", err)
	}
	if _, err := store.TokenStore("two", authTestOtherURL).GetToken(ctx); !errors.Is(err, transport.ErrNoToken) {
		t.Fatalf("GetToken after Logout = %v, want transport.ErrNoToken", err)
	}
}

func TestAuthStoreVaultClearRemovesOrphanSecret(t *testing.T) {
	store, vault, _ := newVaultTestStore(t)
	vault.seed(t, "orphan", authSecretBlob{Token: &transport.Token{AccessToken: "orphan-access"}})

	if err := store.Clear("orphan"); err != nil {
		t.Fatalf("Clear(orphan): %v", err)
	}
	if _, ok := vault.stored(secretNameFor("orphan")); ok {
		t.Fatal("Clear left an orphan vault entry behind")
	}
}

func TestAuthStoreVaultClearDeleteFailure(t *testing.T) {
	store, vault, _ := newVaultTestStore(t)
	if err := store.Save("srv", authTestRecord(t, authTestURL, "access-token")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	vault.setDelErr(errors.New("vault unavailable"))

	if err := store.Clear("srv"); err == nil {
		t.Fatal("Clear must report a vault delete failure")
	}
	if _, ok := vault.stored(secretNameFor("srv")); !ok {
		t.Fatal("the secret must not be deleted when the vault reports a failure")
	}
	if _, ok := store.Record("srv"); !ok {
		t.Fatal("the file record must survive a failed Clear")
	}
}

func TestAuthStoreVaultReadErrorDegradesToAbsent(t *testing.T) {
	logs := captureLogs(t)
	store, vault, _ := newVaultTestStore(t)
	ctx := context.Background()

	if err := store.Save("srv", authTestRecord(t, authTestURL, "access-token")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	vault.setGetErr(errors.New("keyring unavailable"))

	rec, ok := store.Record("srv")
	if !ok {
		t.Fatal("Record must still report the record (only the secrets are absent)")
	}
	if rec.ClientSecret != "" || rec.Token != nil {
		t.Fatalf("Record leaked secrets despite a vault read error: %+v", rec)
	}
	if rec.URL != authTestURL || rec.ClientID != "client-id" || !rec.AuthRequired {
		t.Fatalf("Record lost non-secret fields on a vault read error: %+v", rec)
	}
	if store.Authenticated("srv", authTestURL) {
		t.Fatal("Authenticated must be false when the vault cannot be read")
	}
	if _, err := store.TokenStore("srv", authTestURL).GetToken(ctx); !errors.Is(err, transport.ErrNoToken) {
		t.Fatalf("GetToken = %v, want transport.ErrNoToken", err)
	}
	if out := logs.String(); !strings.Contains(out, "could not be read") {
		t.Fatalf("expected a read warning, got: %q", out)
	}
}

func TestAuthStoreVaultMalformedBlobDegradesToAbsent(t *testing.T) {
	logs := captureLogs(t)
	store, vault, _ := newVaultTestStore(t)
	if err := store.Save("srv", authTestRecord(t, authTestURL, "access-token")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	vault.mu.Lock()
	vault.secrets[secretNameFor("srv")] = "{not json"
	vault.mu.Unlock()

	rec, ok := store.Record("srv")
	if !ok {
		t.Fatal("Record must still report the record")
	}
	if rec.ClientSecret != "" || rec.Token != nil {
		t.Fatalf("a malformed blob produced secrets: %+v", rec)
	}
	if out := logs.String(); !strings.Contains(out, "could not be decoded") {
		t.Fatalf("expected a decode warning, got: %q", out)
	}
}

func TestAuthStoreVaultWriteErrorFailsClosed(t *testing.T) {
	store, vault, path := newVaultTestStore(t)
	ctx := context.Background()

	vault.setFailEvery(errors.New("vault write failed"))
	rec := authTestRecord(t, authTestURL, "access-token")

	if err := store.Save("srv", rec); err == nil {
		t.Fatal("Save must return the vault error")
	}
	if _, ok := vault.stored(secretNameFor("srv")); ok {
		t.Fatal("a failed SetSecret left a vault entry")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a failed Save must not create mcp-auth.json: %v", err)
	}
	if _, ok := store.Record("srv"); ok {
		t.Fatal("a failed Save must not publish a record")
	}

	if err := store.TokenStore("srv", authTestURL).SaveToken(ctx, &transport.Token{AccessToken: "access-token"}); err == nil {
		t.Fatal("SaveToken must return the vault error")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		data, _ := os.ReadFile(path)
		if bytes.Contains(data, []byte("access-token")) {
			t.Fatalf("a failed SaveToken wrote the token to the file: %s", data)
		}
	}
}

func TestAuthStoreVaultEmptySecretsDeleteEntry(t *testing.T) {
	store, vault, _ := newVaultTestStore(t)
	ctx := context.Background()

	if err := store.Save("srv", AuthRecord{URL: authTestURL, ClientID: "client-id"}); err != nil {
		t.Fatalf("Save (registration only): %v", err)
	}
	if names := vault.names(); len(names) != 0 {
		t.Fatalf("a credential-less record stored %v", names)
	}
	if _, _, del := vault.counts(); del == 0 {
		t.Fatal("a credential-less Save must call DeleteSecret (idempotent no-op)")
	}

	if err := store.TokenStore("srv", authTestURL).SaveToken(ctx, &transport.Token{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	if _, ok := vault.stored(secretNameFor("srv")); !ok {
		t.Fatal("SaveToken did not store the token")
	}
	if err := store.TokenStore("srv", authTestURL).SaveToken(ctx, nil); err != nil {
		t.Fatalf("SaveToken(nil): %v", err)
	}
	if names := vault.names(); len(names) != 0 {
		t.Fatalf("SaveToken(nil) left %v behind", names)
	}
	if _, err := store.TokenStore("srv", authTestURL).GetToken(ctx); !errors.Is(err, transport.ErrNoToken) {
		t.Fatalf("GetToken after SaveToken(nil) = %v, want transport.ErrNoToken", err)
	}

	if err := store.TokenStore("srv", authTestURL).SaveToken(ctx, &transport.Token{RefreshToken: "refresh-only"}); err != nil {
		t.Fatalf("SaveToken (refresh only): %v", err)
	}
	if store.Authenticated("srv", authTestURL) {
		t.Fatal("a refresh-token-only record must not count as authenticated")
	}
}

func TestSecretVaultFactoryResolution(t *testing.T) {
	resetSecretVaultFactory(t)

	dir := t.TempDir()
	factoryVault := newFakeVault()
	var gotDirs []string
	var mu sync.Mutex
	RegisterSecretVaultFactory(func(leleDir string) SecretVault {
		mu.Lock()
		gotDirs = append(gotDirs, leleDir)
		mu.Unlock()
		return factoryVault
	})

	store := NewAuthStore(dir)
	if store.vault != SecretVault(factoryVault) {
		t.Fatal("NewAuthStore did not adopt the factory's vault")
	}
	mu.Lock()
	if len(gotDirs) != 1 || gotDirs[0] != dir {
		t.Fatalf("factory called with %v, want [%q]", gotDirs, dir)
	}
	mu.Unlock()

	explicit := newFakeVault()
	overridden := NewAuthStore(dir, WithSecretVault(explicit))
	if overridden.vault != SecretVault(explicit) {
		t.Fatal("WithSecretVault did not override the factory")
	}
	if factoryVault == explicit {
		t.Fatal("test bug: the two vaults must be distinct")
	}

	RegisterSecretVaultFactory(func(string) SecretVault { return nil })
	legacy := NewAuthStore(dir)
	if legacy.vault != nil {
		t.Fatal("a nil factory return must mean no vault")
	}
	rec := authTestRecord(t, authTestURL, "access-token")
	if err := legacy.Save("srv", rec); err != nil {
		t.Fatalf("Save (legacy): %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, authFileName))
	if err != nil {
		t.Fatalf("read auth file: %v", err)
	}
	if !bytes.Contains(data, []byte(`"client_secret":"client-secret"`)) || !bytes.Contains(data, []byte(`"access_token":"access-token"`)) {
		t.Fatalf("legacy mode must keep the credentials in the file: %s", data)
	}

	RegisterSecretVaultFactory(func(string) SecretVault { return factoryVault })
	disabled := NewAuthStore("", WithSecretVault(factoryVault))
	if disabled.vault != nil {
		t.Fatal("a disabled store must not hold a vault")
	}
	if disabled.Enabled() {
		t.Fatal("NewAuthStore(\"\") must be disabled")
	}

	RegisterSecretVaultFactory(nil)
	if cleared := NewAuthStore(dir); cleared.vault != nil {
		t.Fatal("a cleared factory must yield no vault")
	}
}

func TestAuthStoreWithoutVaultKeepsPlaintext(t *testing.T) {
	store, path := newAuthTestStore(t)
	rec := authTestRecord(t, authTestURL, "access-token")

	if err := store.Save("srv", rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Contains(data, []byte(`"client_secret":"client-secret"`)) {
		t.Fatalf("no-vault mode must keep client_secret in the file: %s", data)
	}
	if !bytes.Contains(data, []byte(`"access_token":"access-token"`)) {
		t.Fatalf("no-vault mode must keep the token in the file: %s", data)
	}

	got, ok := store.Record("srv")
	if !ok || got.ClientSecret != "client-secret" || got.Token == nil || got.Token.AccessToken != "access-token" {
		t.Fatalf("Record = %+v (ok=%v)", got, ok)
	}
	if rev := store.Revision("srv"); rev != authRevision(rec) {
		t.Fatalf("Revision = %d, want %d", rev, authRevision(rec))
	}
}
func (v *fakeVault) clearFailFor(server string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.setErrFor, secretNameFor(server))
}

func TestOAuthFlowWithVaultKeepsSecretsOutOfTheFile(t *testing.T) {
	const clientSecret = "confidential-client-secret"

	as := authtest.New(authtest.WithScopes("mcp", "offline_access"))
	as.SetClientSecretSecret(clientSecret)
	asrv := httptest.NewServer(as.Handler())
	t.Cleanup(asrv.Close)
	as.SetIssuer(asrv.URL)

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	dir := t.TempDir()
	vault := newFakeVault()
	store := NewAuthStore(dir, WithSecretVault(vault))
	mcpURL := asrv.URL + authtest.DefaultResourcePath
	ctx := context.Background()

	flow, err := StartAuth(ctx, "srv", ServerConfig{
		URL:  mcpURL,
		Type: typeHTTP,
		Auth: &AuthSpec{Type: typeAuthOAuth},
	}, store, AuthOptions{})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	defer flow.Cancel()

	assertFileHoldsNoSecrets(t, filepath.Join(dir, authFileName), clientSecret)
	blob := vault.blob(t, "srv")
	if blob.ClientSecret != clientSecret {
		t.Fatalf("vault client_secret = %q, want the registrar's secret", blob.ClientSecret)
	}

	completeLoopback(t, noRedirect, flow)
	if err := flow.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	blob = vault.blob(t, "srv")
	if blob.Token == nil || blob.Token.AccessToken == "" {
		t.Fatalf("vault holds no access token after the flow: %+v", blob)
	}
	if blob.ClientSecret != clientSecret {
		t.Fatalf("vault client_secret = %q after the flow, want it preserved", blob.ClientSecret)
	}
	assertFileHoldsNoSecrets(t, filepath.Join(dir, authFileName), clientSecret, blob.Token.AccessToken, blob.Token.RefreshToken)

	if !store.Authenticated("srv", mcpURL) {
		t.Fatal("Authenticated = false after a completed flow")
	}
	tok, err := store.TokenStore("srv", mcpURL).GetToken(ctx)
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if tok.AccessToken != blob.Token.AccessToken {
		t.Fatalf("GetToken returned %q, want the token the vault holds", tok.AccessToken)
	}
	rt := buildAuthRuntime(store, "srv", ServerConfig{URL: mcpURL, Type: typeHTTP})
	if rt == nil {
		t.Fatal("buildAuthRuntime = nil for a server the store knows needs auth")
	}
	if rt.clientSecret != clientSecret {
		t.Fatalf("oauthRuntime client secret = %q, want the vault's secret", rt.clientSecret)
	}
	if rt.clientID == "" {
		t.Fatal("oauthRuntime lost the client id (the non-secret half must stay in the file)")
	}
}
