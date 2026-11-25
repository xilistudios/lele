package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func jsonPathValue(t *testing.T, parsed map[string]interface{}, keys ...string) (interface{}, bool) {
	t.Helper()
	var current interface{} = parsed
	for _, key := range keys {
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		value, ok := object[key]
		if !ok {
			return nil, false
		}
		current = value
	}
	return current, true
}

func requireAbsentJSONPath(t *testing.T, parsed map[string]interface{}, keys ...string) {
	t.Helper()
	if value, ok := jsonPathValue(t, parsed, keys...); ok {
		t.Fatalf("serialized config carries %v = %v, want the key absent (unset must stay unset)", keys, value)
	}
}

func TestGatewayHost_UnsetDefaultsLoopback(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Gateway.Host != DefaultGatewayHost {
		t.Fatalf("DefaultConfig().Gateway.Host = %q, want %q", cfg.Gateway.Host, DefaultGatewayHost)
	}

	path := writeSaveFixture(t, `{}`)
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Gateway.Host != DefaultGatewayHost {
		t.Fatalf("LoadConfig({}).Gateway.Host = %q, want %q", loaded.Gateway.Host, DefaultGatewayHost)
	}
	if host := loaded.EffectiveServerHost(); host != DefaultGatewayHost {
		t.Fatalf("EffectiveServerHost() = %q, want %q", host, DefaultGatewayHost)
	}
}

func TestGatewayHost_ExplicitPreserved(t *testing.T) {
	tests := []struct {
		name     string
		document string
		wantHost string
		wantBind string
		explicit bool
	}{
		{
			name:     "gateway_host_all_interfaces",
			document: `{"gateway":{"host":"0.0.0.0"}}`,
			wantHost: "0.0.0.0",
			wantBind: "0.0.0.0",
			explicit: true,
		},
		{
			name:     "gateway_host_private_address",
			document: `{"gateway":{"host":"192.168.1.5"}}`,
			wantHost: "192.168.1.5",
			wantBind: "192.168.1.5",
			explicit: true,
		},
		{
			name:     "server_host_wins_over_gateway_default",
			document: `{"server":{"host":"10.0.0.7"}}`,
			wantHost: DefaultGatewayHost,
			wantBind: "10.0.0.7",
			explicit: true,
		},
		{
			name:     "unset_stays_loopback",
			document: `{"gateway":{"port":18790}}`,
			wantHost: DefaultGatewayHost,
			wantBind: DefaultGatewayHost,
			explicit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeSaveFixture(t, tt.document)
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.Gateway.Host != tt.wantHost {
				t.Errorf("Gateway.Host = %q, want %q", cfg.Gateway.Host, tt.wantHost)
			}
			if host := cfg.EffectiveServerHost(); host != tt.wantBind {
				t.Errorf("EffectiveServerHost() = %q, want %q", host, tt.wantBind)
			}
			if got := cfg.HostExplicitlyConfigured(); got != tt.explicit {
				t.Errorf("HostExplicitlyConfigured() = %v, want %v", got, tt.explicit)
			}
		})
	}
}

func TestGatewayHost_UnsetSaveReloadStaysUnset(t *testing.T) {
	t.Run("save_config", func(t *testing.T) {
		path := writeSaveFixture(t, `{"language":"en"}`)

		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.Gateway.Host != DefaultGatewayHost {
			t.Fatalf("Gateway.Host = %q, want %q", cfg.Gateway.Host, DefaultGatewayHost)
		}

		if err := SaveConfig(path, cfg); err != nil {
			t.Fatalf("SaveConfig: %v", err)
		}

		requireAbsentJSONPath(t, readSavedJSON(t, path), "gateway", "host")

		reloaded, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig (reload): %v", err)
		}
		if reloaded.Gateway.Host != DefaultGatewayHost {
			t.Errorf("reloaded Gateway.Host = %q, want %q", reloaded.Gateway.Host, DefaultGatewayHost)
		}
		if reloaded.HostExplicitlyConfigured() {
			t.Error("reloaded HostExplicitlyConfigured() = true, want false (the file never named a host)")
		}
	})

	t.Run("save_editable_document", func(t *testing.T) {
		path := writeSaveFixture(t, `{"language":"en"}`)

		doc, _, err := LoadEditableDocument(path)
		if err != nil {
			t.Fatalf("LoadEditableDocument: %v", err)
		}
		if doc.Gateway.Host != DefaultGatewayHost {
			t.Fatalf("doc.Gateway.Host = %q, want %q", doc.Gateway.Host, DefaultGatewayHost)
		}

		if err := SaveEditableDocument(path, doc); err != nil {
			t.Fatalf("SaveEditableDocument: %v", err)
		}

		requireAbsentJSONPath(t, readSavedJSON(t, path), "gateway", "host")

		reloaded, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig (reload): %v", err)
		}
		if reloaded.Gateway.Host != DefaultGatewayHost {
			t.Errorf("reloaded Gateway.Host = %q, want %q", reloaded.Gateway.Host, DefaultGatewayHost)
		}
	})

	t.Run("editable_document_omits_default", func(t *testing.T) {
		path := writeSaveFixture(t, `{"language":"en"}`)

		doc, _, err := LoadEditableDocument(path)
		if err != nil {
			t.Fatalf("LoadEditableDocument: %v", err)
		}

		serializable, err := json.Marshal(doc.toSerializable())
		if err != nil {
			t.Fatalf("marshal serializable: %v", err)
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(serializable, &parsed); err != nil {
			t.Fatalf("unmarshal serializable: %v", err)
		}
		requireAbsentJSONPath(t, parsed, "gateway", "host")
	})

	t.Run("explicit_survives_repeated_saves", func(t *testing.T) {
		path := writeSaveFixture(t, `{"gateway":{"host":"0.0.0.0"}}`)

		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		for i := 0; i < 3; i++ {
			if err := SaveConfig(path, cfg); err != nil {
				t.Fatalf("SaveConfig #%d: %v", i+1, err)
			}
		}

		value, ok := jsonPathValue(t, readSavedJSON(t, path), "gateway", "host")
		if !ok || value != "0.0.0.0" {
			t.Fatalf("saved gateway.host = %v (present %v), want \"0.0.0.0\"", value, ok)
		}
	})
}

func TestHostExplicitlyConfigured(t *testing.T) {
	tests := []struct {
		name     string
		document string
		env      map[string]string
		want     bool
	}{
		{name: "unset", document: `{}`, want: false},
		{name: "gateway_host", document: `{"gateway":{"host":"127.0.0.1"}}`, want: true},
		{name: "server_host", document: `{"server":{"host":"0.0.0.0"}}`, want: true},
		{name: "native_host", document: `{"channels":{"native":{"host":"0.0.0.0"}}}`, want: true},
		{name: "null_host_is_unset", document: `{"gateway":{"host":null}}`, want: false},
		{
			name:     "gateway_host_env",
			document: `{}`,
			env:      map[string]string{"LELE_GATEWAY_HOST": "0.0.0.0"},
			want:     true,
		},
		{
			name:     "empty_env_is_unset",
			document: `{}`,
			env:      map[string]string{"LELE_GATEWAY_HOST": ""},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for name, value := range tt.env {
				t.Setenv(name, value)
			}
			path := writeSaveFixture(t, tt.document)
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if got := cfg.HostExplicitlyConfigured(); got != tt.want {
				t.Errorf("HostExplicitlyConfigured() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("missing_file_is_unset", func(t *testing.T) {
		cfg, err := LoadConfig(filepath.Join(t.TempDir(), "absent.json"))
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.HostExplicitlyConfigured() {
			t.Error("HostExplicitlyConfigured() = true for a missing config file, want false")
		}
	})
}

func TestEffectiveServerHost_FallbackLoopback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Host = ""
	cfg.Gateway.Host = ""
	cfg.Channels.Native.Host = ""

	if host := cfg.EffectiveServerHost(); host != DefaultGatewayHost {
		t.Fatalf("EffectiveServerHost() with every host empty = %q, want %q", host, DefaultGatewayHost)
	}

	cfg.Channels.Native.Host = "127.0.0.2"
	if host := cfg.EffectiveServerHost(); host != "127.0.0.2" {
		t.Fatalf("EffectiveServerHost() = %q, want the native host", host)
	}

	cfg.Gateway.Host = "0.0.0.0"
	if host := cfg.EffectiveServerHost(); host != "0.0.0.0" {
		t.Fatalf("EffectiveServerHost() = %q, want the gateway host to win over the native host", host)
	}

	cfg.Server.Host = "10.0.0.7"
	if host := cfg.EffectiveServerHost(); host != "10.0.0.7" {
		t.Fatalf("EffectiveServerHost() = %q, want the server host to win", host)
	}
}

func TestHostExplicitlyConfiguredSurvivesSnapshot(t *testing.T) {
	path := writeSaveFixture(t, `{"gateway":{"host":"0.0.0.0"}}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if !cfg.Snapshot().HostExplicitlyConfigured() {
		t.Error("Snapshot().HostExplicitlyConfigured() = false, want the loaded flag carried over")
	}

	if err := cfg.Reload(path); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !cfg.HostExplicitlyConfigured() {
		t.Error("HostExplicitlyConfigured() = false after Reload, want true")
	}

	other := writeSaveFixture(t, `{}`)
	if err := cfg.Reload(other); err != nil {
		t.Fatalf("Reload (unset host): %v", err)
	}
	if cfg.HostExplicitlyConfigured() {
		t.Error("HostExplicitlyConfigured() = true after reloading a file without a host, want false")
	}
}
