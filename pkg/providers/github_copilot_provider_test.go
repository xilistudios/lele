package providers

import "testing"

func TestGitHubCopilotProvider_New_StdioMode(t *testing.T) {
	// "stdio" connect mode doesn't attempt to spawn a CLI in this implementation,
	// so it returns a provider with a nil session without error.
	p, err := NewGitHubCopilotProvider("/tmp/cli", "stdio", "gpt-4.1")
	if err != nil {
		t.Fatalf("NewGitHubCopilotProvider(stdio) error: %v", err)
	}
	if p == nil {
		t.Fatal("NewGitHubCopilotProvider(stdio) returned nil")
	}
	if p.connectMode != "stdio" {
		t.Errorf("connectMode = %q, want stdio", p.connectMode)
	}
	if p.uri != "/tmp/cli" {
		t.Errorf("uri = %q, want /tmp/cli", p.uri)
	}
}

func TestGitHubCopilotProvider_GetDefaultModel(t *testing.T) {
	p := &GitHubCopilotProvider{}
	if got := p.GetDefaultModel(); got != "gpt-4.1" {
		t.Errorf("GetDefaultModel() = %q, want gpt-4.1", got)
	}
}
