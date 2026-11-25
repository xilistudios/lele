package config

import "testing"

func TestGatewayHost_EditableSaveKeepsExplicitLoopback(t *testing.T) {
	tests := []struct {
		name        string
		document    string
		setLoopback func(*EditableDocument)
		path        []string
	}{
		{
			name:        "gateway_host",
			document:    `{"gateway":{"host":"0.0.0.0"}}`,
			setLoopback: func(doc *EditableDocument) { doc.Gateway.Host = DefaultGatewayHost },
			path:        []string{"gateway", "host"},
		},
		{
			name:        "native_host",
			document:    `{"channels":{"native":{"host":"0.0.0.0"}}}`,
			setLoopback: func(doc *EditableDocument) { doc.Channels.Native.Host = DefaultGatewayHost },
			path:        []string{"channels", "native", "host"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeSaveFixture(t, tt.document)

			doc, _, err := LoadEditableDocument(path)
			if err != nil {
				t.Fatalf("LoadEditableDocument: %v", err)
			}
			tt.setLoopback(doc)
			if err := SaveEditableDocument(path, doc); err != nil {
				t.Fatalf("SaveEditableDocument: %v", err)
			}

			value, present := jsonPathValue(t, readSavedJSON(t, path), tt.path...)
			if !present || value != DefaultGatewayHost {
				t.Fatalf("saved %v = %v (present %v), want the explicitly named %q kept", tt.path, value, present, DefaultGatewayHost)
			}

			reloaded, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig after save: %v", err)
			}
			if !reloaded.HostExplicitlyConfigured() {
				t.Errorf("HostExplicitlyConfigured() = false, want true: the WebUI save removed a host the file named")
			}
		})
	}
}

func TestGatewayHost_ExplicitLoopbackSurvivesRepeatedEditableSaves(t *testing.T) {
	path := writeSaveFixture(t, `{"gateway":{"host":"0.0.0.0"}}`)

	for i := 0; i < 3; i++ {
		doc, _, err := LoadEditableDocument(path)
		if err != nil {
			t.Fatalf("LoadEditableDocument #%d: %v", i+1, err)
		}
		doc.Gateway.Host = DefaultGatewayHost
		if err := SaveEditableDocument(path, doc); err != nil {
			t.Fatalf("SaveEditableDocument #%d: %v", i+1, err)
		}

		value, present := jsonPathValue(t, readSavedJSON(t, path), "gateway", "host")
		if !present || value != DefaultGatewayHost {
			t.Fatalf("save #%d: gateway.host = %v (present %v), want %q", i+1, value, present, DefaultGatewayHost)
		}
	}
}

func TestGatewayHost_EditableSaveDoesNotMaterializeUnsetHost(t *testing.T) {
	path := writeSaveFixture(t, `{"language":"en"}`)

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	doc.Agents.Defaults.MaxTokens = 4096
	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument: %v", err)
	}

	requireAbsentJSONPath(t, readSavedJSON(t, path), "gateway", "host")

	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if reloaded.HostExplicitlyConfigured() {
		t.Error("HostExplicitlyConfigured() = true, want false: the file never named a host")
	}
}

func TestServerHost_EditableSavePreservesExplicitHost(t *testing.T) {
	tests := []struct {
		name string
		host string
	}{
		{name: "loopback_named", host: DefaultGatewayHost},
		{name: "empty_named", host: ""},
		{name: "all_interfaces_named", host: "0.0.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeSaveFixture(t, `{"server":{"host":"`+tt.host+`"},"language":"en"}`)

			doc, _, err := LoadEditableDocument(path)
			if err != nil {
				t.Fatalf("LoadEditableDocument: %v", err)
			}
			doc.Agents.Defaults.MaxTokens = 4096
			if err := SaveEditableDocument(path, doc); err != nil {
				t.Fatalf("SaveEditableDocument: %v", err)
			}

			value, present := jsonPathValue(t, readSavedJSON(t, path), "server", "host")
			if !present || value != tt.host {
				t.Fatalf("saved server.host = %v (present %v), want the named %q kept", value, present, tt.host)
			}

			reloaded, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig after save: %v", err)
			}
			if !reloaded.HostExplicitlyConfigured() {
				t.Error("HostExplicitlyConfigured() = false, want true: the file still names server.host")
			}
		})
	}
}
