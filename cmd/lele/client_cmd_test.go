package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

func newAuthManager(t *testing.T) *channels.AuthManager {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.NativeConfig{Enabled: true, TokenExpiryDays: 30, PinExpiryMinutes: 5, MaxClients: 5}
	am, err := channels.NewAuthManager(cfg, dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	return am
}

func TestClientPinCmd_Success(t *testing.T) {
	am := newAuthManager(t)
	replaceArgs(t, []string{"lele", "client", "pin"})
	out := runCmd(func() { clientPinCmd(am) })
	if !strings.Contains(out, "Pairing PIN Generated") {
		t.Errorf("expected PIN generated message, got: %s", out)
	}
	if !strings.Contains(out, "PIN:") {
		t.Errorf("expected PIN output, got: %s", out)
	}
}

func TestClientPinCmd_WithDevice(t *testing.T) {
	am := newAuthManager(t)
	replaceArgs(t, []string{"lele", "client", "pin", "--device", "My Laptop"})
	out := runCmd(func() { clientPinCmd(am) })
	if !strings.Contains(out, "Pairing PIN Generated") {
		t.Errorf("expected PIN generated message, got: %s", out)
	}
}

func TestClientListCmd_Empty(t *testing.T) {
	am := newAuthManager(t)
	out := runCmd(func() { clientListCmd(am) })
	if !strings.Contains(out, "No paired clients") {
		t.Errorf("expected no clients message, got: %s", out)
	}
}

func TestClientListCmd_WithClients(t *testing.T) {
	am := newAuthManager(t)
	// Generate a PIN and convert it to a paired client.
	p, err := am.GeneratePIN("test-device")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}
	client, _, _, err := am.PairWithPIN(p.PIN, "test-device")
	if err != nil {
		t.Fatalf("PairWithPIN: %v", err)
	}
	_ = client
	out := runCmd(func() { clientListCmd(am) })
	if !strings.Contains(out, "Paired Clients") {
		t.Errorf("expected heading, got: %s", out)
	}
	if !strings.Contains(out, "test-device") {
		t.Errorf("expected device name, got: %s", out)
	}
}

func TestClientPendingCmd_Empty(t *testing.T) {
	am := newAuthManager(t)
	out := runCmd(func() { clientPendingCmd(am) })
	if !strings.Contains(out, "No pending pairing requests") {
		t.Errorf("expected no pending message, got: %s", out)
	}
}

func TestClientPendingCmd_WithPending(t *testing.T) {
	am := newAuthManager(t)
	if _, err := am.GeneratePIN("phone-device"); err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}
	out := runCmd(func() { clientPendingCmd(am) })
	if !strings.Contains(out, "Pending Pairing Requests") {
		t.Errorf("expected heading, got: %s", out)
	}
	if !strings.Contains(out, "phone-device") {
		t.Errorf("expected device name, got: %s", out)
	}
}

func TestClientRemoveCmd_Found(t *testing.T) {
	am := newAuthManager(t)
	p, err := am.GeneratePIN("remove-me")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}
	client, _, _, err := am.PairWithPIN(p.PIN, "remove-me")
	if err != nil {
		t.Fatalf("PairWithPIN: %v", err)
	}
	out := runCmd(func() { clientRemoveCmd(am, client.ClientID) })
	if !strings.Contains(out, "removed") {
		t.Errorf("expected removal message, got: %s", out)
	}
}

func TestClientStatusCmd(t *testing.T) {
	am := newAuthManager(t)
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	temp := 0.7
	cfg.Agents.Defaults.Temperature = &temp
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.MaxClients = 3
	cfg.Channels.Native.TokenExpiryDays = 7
	cfg.Channels.Native.PinExpiryMinutes = 2
	if err := config.SaveConfig(filepath.Join(dir, "config.json"), cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	out := runCmd(func() { clientStatusCmd(am, cfg) })
	for _, want := range []string{"Client Channel Status", "Max Clients: 3", "Token Expiry: 7 days", "PIN Expiry: 2 minutes"} {
		if !strings.Contains(strings.Join(strings.Fields(out), " "), want) {
			t.Errorf("clientStatusCmd missing %q, got: %s", want, out)
		}
	}
}

func TestClientStatusCmd_WithSQLiteClients(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.NativeConfig{Enabled: true, TokenExpiryDays: 30, PinExpiryMinutes: 5, MaxClients: 5}
	am, err := channels.NewAuthManager(cfg, dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	s, err := store.Open(filepath.Join(dir, "lele.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	am.SetStore(s.NativeClients())

	p, err := am.GeneratePIN("sqlite-client")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}
	_, _, _, err = am.PairWithPIN(p.PIN, "sqlite-client")
	if err != nil {
		t.Fatalf("PairWithPIN: %v", err)
	}
	cfg2, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	temp := 0.7
	cfg2.Agents.Defaults.Temperature = &temp
	out := runCmd(func() { clientStatusCmd(am, cfg2) })
	if !strings.Contains(out, "Paired Clients: 1") {
		t.Errorf("expected 1 paired client, got: %s", out)
	}
}

var _ = os.Getenv