package keyring

type Store interface {
	Open(key []byte) error
	Close() error
	IsOpen() bool
	Set(secret *Secret) error
	Get(name string) (*Secret, error)
	Delete(name string) (bool, error)
	List() ([]SecretMeta, error)
	Search(query string) ([]SecretMeta, error)
	Flush() error
	Backend() string
}
