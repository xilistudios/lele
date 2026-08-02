package keyring

// Store is the persistence backend for secrets. A store holds the decrypted
// vault in memory while open and re-encrypts it on flush.
type Store interface {
	// Open decrypts the vault using the provided 32-byte key. If the vault
	// file does not exist yet, an empty vault is created in memory.
	Open(key []byte) error
	// Close zeroes the in-memory key and clears the decrypted secrets.
	Close() error
	// IsOpen returns true if the vault is decrypted and ready.
	IsOpen() bool
	// Set stores or updates a secret.
	Set(secret *Secret) error
	// Get retrieves a secret by name (including its value).
	Get(name string) (*Secret, bool)
	// Delete removes a secret by name. It returns false if not found.
	Delete(name string) bool
	// List returns metadata for all secrets (no values), sorted by name.
	List() []SecretMeta
	// Search finds secrets matching a query across name, tags, and description.
	Search(query string) []SecretMeta
	// Flush persists changes to disk (re-encrypts the vault).
	Flush() error
	// Backend returns a human-readable description of the storage location.
	Backend() string
}
