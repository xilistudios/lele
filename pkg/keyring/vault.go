package keyring

import "errors"

// systemScope is the scope attached to secrets written through OpaqueVault.
// A non-empty scope restricts the secret to the listed agent IDs only, so
// "system" — a name no agent uses — hides these internal secrets from
// agent-facing access (GetForAgent returns ErrAccessDenied and ListForAgent
// omits them) while leaving GetRaw, ListAll, Search and the UIs untouched.
const systemScope = "system"

// errNilService is returned by every OpaqueVault method when no Service was
// injected. It is a programming error, kept as an error (not a panic) so a
// misconfigured subsystem degrades instead of taking the process down.
var errNilService = errors.New("keyring: nil service")

// OpaqueVault adapts a Service to a minimal Get/Set/Delete string vault for
// internal subsystems (e.g. MCP auth token storage). Values are opaque strings:
// they are stored, replaced and deleted whole, with no structure imposed.
//
// It exists so those subsystems can depend on a narrow interface instead of
// reaching into the keyring's secret model, scope rules or audit trail.
type OpaqueVault struct {
	svc    *Service
	source string
}

// NewOpaqueVault wraps svc. source names the subsystem ("mcp", ...) and is
// recorded as the secret's CreatedBy, so the UIs can show who owns an internal
// secret.
func NewOpaqueVault(svc *Service, source string) *OpaqueVault {
	return &OpaqueVault{svc: svc, source: source}
}

// GetSecret returns the stored value for name. A secret that is not stored is
// NOT an error: it yields ("", nil), so callers can treat absence as "not
// authenticated yet" without inspecting keyring errors. Every other failure
// (ErrDisabled, a vault that cannot be refreshed) propagates unchanged.
//
// GetRaw is used deliberately: this is internal subsystem access, not agent
// access, so the scope check does not apply.
func (v *OpaqueVault) GetSecret(name string) (string, error) {
	if v == nil || v.svc == nil {
		return "", errNilService
	}
	val, err := v.svc.GetRaw(name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return val, nil
}

// SetSecret stores or replaces name with value (an upsert): an existing secret
// keeps its CreatedAt/CreatedBy lineage while Value and UpdatedAt are replaced.
//
// The secret is written with scope ["system"] so it stays invisible to agents
// but visible in the UIs (see systemScope).
func (v *OpaqueVault) SetSecret(name, value string) error {
	if v == nil || v.svc == nil {
		return errNilService
	}
	return v.svc.SetFromUI(name, value, v.description(), nil, []string{systemScope}, v.source)
}

// DeleteSecret removes name. Deleting a secret that is not stored is not an
// error, so delete is idempotent; other failures propagate.
func (v *OpaqueVault) DeleteSecret(name string) error {
	if v == nil || v.svc == nil {
		return errNilService
	}
	err := v.svc.DeleteFromUI(name, v.source)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// description is the human-readable purpose shown by the secret UIs.
func (v *OpaqueVault) description() string {
	if v.source == "" {
		return "Internal secret"
	}
	return "Stored by " + v.source
}
