package mcp

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/xilistudios/lele/pkg/logger"
)

// This file is the seam that lets the CREDENTIAL half of an OAuth record live
// in an external vault (a keyring, an OS secret service, a file the caller
// owns) while the REST of the record stays in mcp-auth.json.
//
// Why a seam and not an import: pkg/mcp sits at the bottom of the dependency
// graph (see the BOUNDARY note in auth_store.go) and must not import
// pkg/keyring — the implementation the production caller wires in. The
// interface below is the whole contract, and the factory registration is the
// same inversion pkg/config uses for its keyring resolver
// (config.RegisterKeyringResolver): the process that owns both sides
// (cmd/lele) registers a builder at startup, and pkg/mcp asks for one by
// directory, exactly the way it asks for a path.
//
// The vault is OPTIONAL in every sense: an unregistered factory, a factory
// returning nil, or a disabled store all mean "no vault", and that mode is
// byte-for-byte the pre-vault behaviour — mcp-auth.json holds everything.

// SecretVault is an opaque string-secret store injected by the caller (e.g. a
// keyring-backed adapter). pkg/mcp never imports the implementation.
//
// Contract:
//
//   - GetSecret returns ("", nil) when the secret is absent. Absence is NOT an
//     error, so a caller can treat "not stored" as "not authenticated yet".
//   - SetSecret is an upsert: a missing secret is created, an existing one is
//     replaced whole.
//   - DeleteSecret is idempotent: deleting a secret that is not stored is not
//     an error.
//   - Any other failure is returned as an error and treated as fatal for the
//     operation that caused it.
//
// Implementations must be safe for concurrent use: an AuthStore calls into the
// vault from every goroutine that reads or writes auth state.
type SecretVault interface {
	GetSecret(name string) (string, error)
	SetSecret(name, value string) error
	DeleteSecret(name string) error
}

var (
	secretVaultFactoryMu sync.RWMutex
	secretVaultFactory   func(leleDir string) SecretVault
)

// RegisterSecretVaultFactory installs the function NewAuthStore uses to obtain
// a vault for a lele directory. Passing nil clears it (no vault ⇒ every store
// runs in legacy plaintext mode).
//
// It must be called ONCE at process startup, before any AuthStore is created:
// a store resolves its vault in the constructor and keeps it for its lifetime,
// so a factory registered later has no effect on stores that already exist.
// The factory may return nil for a directory it cannot serve (no keyring on
// this machine, a locked vault), which is handled as "no vault".
//
// A nil-returning factory is not an error path: it is how a platform without a
// secret service keeps working. The interesting case is deliberately NOT
// special-cased — once a store HAS a vault, writes never fall back to
// plaintext (see AuthStore.Save).
func RegisterSecretVaultFactory(f func(leleDir string) SecretVault) {
	secretVaultFactoryMu.Lock()
	secretVaultFactory = f
	secretVaultFactoryMu.Unlock()
}

// resolveSecretVault consults the registered factory. No factory (or a nil
// return) yields nil: the store then keeps every field in mcp-auth.json.
func resolveSecretVault(leleDir string) SecretVault {
	secretVaultFactoryMu.RLock()
	f := secretVaultFactory
	secretVaultFactoryMu.RUnlock()
	if f == nil {
		return nil
	}
	return f(leleDir)
}

// secretNameFor is the vault key of one server's secrets:
// "mcp.<server>.auth". Server names are the keys of mcp.json, and vault names
// are free-form strings, so no escaping is needed or attempted.
//
// No value ever appears in the name and no structure is encoded in it beyond
// the server; the payload shape lives in authSecretBlob below.
func secretNameFor(server string) string {
	return "mcp." + server + ".auth"
}

// authSecretBlob is the JSON payload stored under secretNameFor(server): the
// ONLY two fields of an AuthRecord that are credentials.
//
//	{"client_secret": "...", "token": {...transport.Token...}}
//
// Both members are omitempty, so a registration-only record stores
// {"client_secret":"..."} and a token-only record stores {"token":{...}} —
// and an EMPTY blob (neither member) means "no secrets" and is deleted rather
// than stored, so an absent vault entry is the canonical "nothing secret here".
//
// ClientID is deliberately NOT in the blob: it is the public half of a dynamic
// registration (it travels in the authorization request), so it stays in
// mcp-auth.json next to the URL it belongs to.
type authSecretBlob struct {
	ClientSecret string           `json:"client_secret,omitempty"`
	Token        *transport.Token `json:"token,omitempty"`
}

// isEmpty reports whether the blob carries no credential at all, i.e. the
// vault entry for this server should not exist.
func (b authSecretBlob) isEmpty() bool {
	return b.ClientSecret == "" && b.Token == nil
}

// encodeAuthSecretBlob marshals blob for storage. transport.Token marshals with
// the json tags mcp-go already defines, so the payload is readable and stable.
func encodeAuthSecretBlob(blob authSecretBlob) (string, error) {
	data, err := json.Marshal(blob)
	if err != nil {
		// Cannot carry a value: json.Marshal reports the Go type only, and
		// every field of the blob is a string or a *transport.Token.
		return "", fmt.Errorf("mcp: encode auth secret: %w", err)
	}
	return string(data), nil
}

// parseAuthSecretBlob decodes a stored payload. An empty payload is "absent"
// (no error, nothing to report); a MALFORMED payload is reported to the caller
// as (zero, false) after a warning, because the read paths that consume it
// cannot return an error and the safe degradation is "no secrets", which leads
// to a re-authentication rather than to a wrong credential being presented.
func parseAuthSecretBlob(server, raw string) (authSecretBlob, bool) {
	if raw == "" {
		return authSecretBlob{}, false
	}
	var blob authSecretBlob
	if err := json.Unmarshal([]byte(raw), &blob); err != nil {
		logWarnValueFree("stored OAuth secret could not be decoded; treating the server as not authenticated", server, err)
		return authSecretBlob{}, false
	}
	return blob, true
} // logWarnValueFree reports a vault problem on the package logger without ever
// naming a value: the server name is a config key, and an error contributes its
// MESSAGE only (vault errors describe the store — a missing keyring, a locked
// file — never the secret they were asked about).
func logWarnValueFree(msg, server string, err error) {
	fields := map[string]interface{}{}
	if server != "" {
		fields["server"] = server
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	logger.WarnCF("mcp", msg, fields)
}
