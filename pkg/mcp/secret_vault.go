package mcp

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/xilistudios/lele/pkg/logger"
)

type SecretVault interface {
	GetSecret(name string) (string, error)
	SetSecret(name, value string) error
	DeleteSecret(name string) error
}

var (
	secretVaultFactoryMu sync.RWMutex
	secretVaultFactory   func(leleDir string) SecretVault
)

func RegisterSecretVaultFactory(f func(leleDir string) SecretVault) {
	secretVaultFactoryMu.Lock()
	secretVaultFactory = f
	secretVaultFactoryMu.Unlock()
}

func resolveSecretVault(leleDir string) SecretVault {
	secretVaultFactoryMu.RLock()
	f := secretVaultFactory
	secretVaultFactoryMu.RUnlock()
	if f == nil {
		return nil
	}
	return f(leleDir)
}

func secretNameFor(server string) string {
	return "mcp." + server + ".auth"
}

type authSecretBlob struct {
	ClientSecret string           `json:"client_secret,omitempty"`
	Token        *transport.Token `json:"token,omitempty"`
}

func (b authSecretBlob) isEmpty() bool {
	return b.ClientSecret == "" && b.Token == nil
}

func encodeAuthSecretBlob(blob authSecretBlob) (string, error) {
	data, err := json.Marshal(blob)
	if err != nil {
		return "", fmt.Errorf("mcp: encode auth secret: %w", err)
	}
	return string(data), nil
}

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
}
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
