package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func loadGatewayWarningConfig(t *testing.T, document string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(document), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

func warningMessages(warnings []gatewayBindWarning) []string {
	messages := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		messages = append(messages, warning.message)
	}
	return messages
}

func findWarningContaining(warnings []gatewayBindWarning, fragment string) *gatewayBindWarning {
	for i := range warnings {
		if strings.Contains(warnings[i].message, fragment) {
			return &warnings[i]
		}
	}
	return nil
}

func TestGatewayStartup_NonLoopbackWarns(t *testing.T) {
	cfg := loadGatewayWarningConfig(t, `{}`)
	cfg.Channels.Native.Enabled = true
	if !cfg.Channels.Native.RateLimit.Enabled {
		t.Fatal("rate limiting is off by default; the warning must not depend on it")
	}

	warnings := gatewayBindWarnings(cfg, "0.0.0.0", 18790, false, true)

	exposure := findWarningContaining(warnings, "reachable from the network")
	if exposure == nil {
		t.Fatalf("no network-exposure warning for a 0.0.0.0 bind with rate limiting on; warnings = %v", warningMessages(warnings))
	}
	if exposure.component != "native" {
		t.Errorf("warning component = %q, want %q", exposure.component, "native")
	}
	if exposure.fields["host"] != "0.0.0.0" {
		t.Errorf("warning host field = %v, want 0.0.0.0", exposure.fields["host"])
	}
	if exposure.fields["rate_limit_enabled"] != true {
		t.Errorf("warning rate_limit_enabled = %v, want true", exposure.fields["rate_limit_enabled"])
	}
	if !strings.Contains(exposure.message, "rate limiting enabled") {
		t.Errorf("warning message = %q, want it to say rate limiting is enabled", exposure.message)
	}
	if unsetHost := findWarningContaining(warnings, unsetHostWarningFragment); unsetHost != nil {
		t.Errorf("a non-loopback bind also emitted the unset-host notice: %q", unsetHost.message)
	}
}

func TestGatewayStartup_NonLoopbackWarnsWithRateLimitDisabled(t *testing.T) {
	cfg := loadGatewayWarningConfig(t, `{"channels":{"native":{"rate_limit":{"enabled":false}}}}`)
	cfg.Channels.Native.Enabled = true

	warnings := gatewayBindWarnings(cfg, "0.0.0.0", 18790, false, true)

	exposure := findWarningContaining(warnings, "reachable from the network")
	if exposure == nil {
		t.Fatalf("no network-exposure warning with rate limiting off; warnings = %v", warningMessages(warnings))
	}
	if !strings.Contains(exposure.message, "rate limiting disabled") {
		t.Errorf("warning message = %q, want it to say rate limiting is disabled", exposure.message)
	}
}

const unsetHostWarningFragment = "gateway.host is unset"

func TestGatewayStartup_UnsetHostWarns(t *testing.T) {
	cfg := loadGatewayWarningConfig(t, `{"language":"en"}`)
	serverHost := cfg.EffectiveServerHost()
	if serverHost != config.DefaultGatewayHost {
		t.Fatalf("EffectiveServerHost() = %q, want the loopback default", serverHost)
	}

	warnings := gatewayBindWarnings(cfg, serverHost, 18790, false, true)

	unsetHost := findWarningContaining(warnings, unsetHostWarningFragment)
	if unsetHost == nil {
		t.Fatalf("no unset-host notice for a config file that names no host; warnings = %v", warningMessages(warnings))
	}
	if unsetHost.component != "server" {
		t.Errorf("warning component = %q, want %q", unsetHost.component, "server")
	}
	want := "gateway.host is unset; lele binds loopback (127.0.0.1) by default. Set gateway.host explicitly (e.g. \"0.0.0.0\") for LAN access."
	if unsetHost.message != want {
		t.Errorf("warning message = %q, want %q", unsetHost.message, want)
	}
	if exposure := findWarningContaining(warnings, "reachable from the network"); exposure != nil {
		t.Errorf("loopback bind also emitted the network-exposure warning: %q", exposure.message)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %v, want exactly one", warningMessages(warnings))
	}
}

func TestGatewayStartup_UnsetHostWarnsOnFreshInstallFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.DefaultConfig()
	if err := config.SaveMinimalConfig(path, cfg); err != nil {
		t.Fatalf("SaveMinimalConfig: %v", err)
	}

	loaded, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.HostExplicitlyConfigured() {
		t.Fatal("the onboarding writer materialized a host; the notice would never fire for a fresh install")
	}
	if host := loaded.EffectiveServerHost(); host != config.DefaultGatewayHost {
		t.Fatalf("EffectiveServerHost() = %q, want the loopback default", host)
	}

	warnings := gatewayBindWarnings(loaded, loaded.EffectiveServerHost(), 18790, false, configFilePresent(path))

	unsetHost := findWarningContaining(warnings, unsetHostWarningFragment)
	if unsetHost == nil {
		t.Fatalf("no unset-host notice for an onboarding-written config; warnings = %v", warningMessages(warnings))
	}
	if strings.Contains(unsetHost.message, "changed") {
		t.Errorf("warning message = %q, want no claim that anything changed (a fresh install sees it too)", unsetHost.message)
	}
	if exposure := findWarningContaining(warnings, "reachable from the network"); exposure != nil {
		t.Errorf("loopback bind also emitted the network-exposure warning: %q", exposure.message)
	}
}

func TestGatewayStartup_NoWarningAfterEditableSaveOfExplicitLoopback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"gateway":{"host":"0.0.0.0"}}`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	doc, _, err := config.LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	doc.Gateway.Host = config.DefaultGatewayHost
	if err := config.SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument: %v", err)
	}

	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg.Channels.Native.Enabled = true

	warnings := gatewayBindWarnings(cfg, cfg.EffectiveServerHost(), 18790, false, configFilePresent(path))

	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none: the operator explicitly chose loopback in the settings UI", warningMessages(warnings))
	}
}

func TestGatewayStartup_NoWarningWhenHostExplicit(t *testing.T) {
	cfg := loadGatewayWarningConfig(t, `{"gateway":{"host":"127.0.0.1"}}`)

	warnings := gatewayBindWarnings(cfg, cfg.EffectiveServerHost(), 18790, false, true)

	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none for an explicitly configured loopback host", warningMessages(warnings))
	}
}

func TestGatewayStartup_ExplicitLANBindKeepsReachability(t *testing.T) {
	cfg := loadGatewayWarningConfig(t, `{"gateway":{"host":"0.0.0.0"}}`)
	cfg.Channels.Native.Enabled = true

	warnings := gatewayBindWarnings(cfg, cfg.EffectiveServerHost(), 18790, false, true)

	if findWarningContaining(warnings, "defaults to 127.0.0.1") != nil {
		t.Fatalf("explicit 0.0.0.0 emitted the default-changed warning; warnings = %v", warningMessages(warnings))
	}
	if findWarningContaining(warnings, "reachable from the network") == nil {
		t.Fatalf("explicit 0.0.0.0 bind did not warn about network reachability; warnings = %v", warningMessages(warnings))
	}
}

func TestGatewayStartup_NoWarningForNewInstall(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true

	warnings := gatewayBindWarnings(cfg, cfg.EffectiveServerHost(), 18790, false, false)

	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none for a config-less install (silent loopback)", warningMessages(warnings))
	}
}

func TestGatewayStartup_NoWarningForDesktop(t *testing.T) {
	cfg := loadGatewayWarningConfig(t, `{"language":"en"}`)
	cfg.Channels.Native.Enabled = true
	if cfg.HostExplicitlyConfigured() {
		t.Fatal("the fixture must leave the host unset for the desktop check to mean anything")
	}

	warnings := gatewayBindWarnings(cfg, config.DefaultGatewayHost, 0, true, true)

	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none in desktop mode (loopback is forced, the default-changed warning is noise)", warningMessages(warnings))
	}
}

func TestGatewayStartup_NoWarningWhenNativeDisabled(t *testing.T) {
	cfg := loadGatewayWarningConfig(t, `{"gateway":{"host":"0.0.0.0"},"channels":{"native":{"enabled":false}}}`)

	warnings := gatewayBindWarnings(cfg, cfg.EffectiveServerHost(), 18790, false, true)

	if findWarningContaining(warnings, "reachable from the network") != nil {
		t.Fatalf("native channel disabled still warned about exposure; warnings = %v", warningMessages(warnings))
	}
}

func TestConfigFilePresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if configFilePresent(path) {
		t.Fatal("configFilePresent() = true for a missing file, want false")
	}
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if !configFilePresent(path) {
		t.Fatal("configFilePresent() = false for an existing file, want true")
	}
}
