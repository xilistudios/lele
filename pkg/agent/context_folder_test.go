package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// newFolderTestBuilder returns a ContextBuilder over a throwaway workspace and
// a separate folder the "user" can select for a session.
func newFolderTestBuilder(t *testing.T) (cb *ContextBuilder, folder string) {
	t.Helper()

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENT.md"), []byte("# agent"), 0o644); err != nil {
		t.Fatalf("write AGENT.md: %v", err)
	}

	folder = t.TempDir()
	if err := os.Mkdir(filepath.Join(folder, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(folder, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	return NewContextBuilder(workspace), folder
}

// TestBuildMessagesInjectsFolderContext is the end-to-end check of the feature:
// a session with a selected folder gets the "## Selected Folder" section (path
// + first-level listing) in the system prompt sent to the LLM.
func TestBuildMessagesInjectsFolderContext(t *testing.T) {
	cb, folder := newFolderTestBuilder(t)
	const sessionKey = "native:client-1:conv-1"

	cb.SetFolderResolver(func(sk string) string {
		if sk == sessionKey {
			return folder
		}
		return ""
	})

	messages := cb.BuildMessages([]providers.Message{}, "", "hello", nil, "native", "native:client-1", sessionKey, "")
	if len(messages) == 0 {
		t.Fatal("expected at least the system message")
	}

	prompt := messages[0].Content
	for _, want := range []string{
		"## Selected Folder",
		"Folder: `" + folder + "`",
		"### Directory Listing (First-Level)",
		"- src/",
		"- main.go",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}

	// A different session must not see the folder.
	other := cb.BuildMessages([]providers.Message{}, "", "hello", nil, "native", "native:client-1", "native:client-1:other", "")
	if strings.Contains(other[0].Content, "## Selected Folder") {
		t.Error("folder context leaked into a session without a selected folder")
	}
}

// TestBuildMessagesWithoutFolderResolver pins the negative case: with no
// resolver wired (every non-WebUI deployment today) the prompt is unchanged.
func TestBuildMessagesWithoutFolderResolver(t *testing.T) {
	cb, _ := newFolderTestBuilder(t)

	messages := cb.BuildMessages([]providers.Message{}, "", "hello", nil, "native", "native:client-1", "native:client-1:conv", "")
	if strings.Contains(messages[0].Content, "## Selected Folder") {
		t.Error("nil resolver must not inject folder context")
	}

	// An empty resolver answer behaves the same as no resolver.
	cb.SetFolderResolver(func(string) string { return "" })
	messages = cb.BuildMessages([]providers.Message{}, "", "hello", nil, "native", "native:client-1", "native:client-1:conv", "")
	if strings.Contains(messages[0].Content, "## Selected Folder") {
		t.Error("empty folder must not inject folder context")
	}
}

// TestBuildSystemPromptForSessionWithFolder checks the public entry point used
// by the token-estimation call sites, and that the folder section is appended
// after the base prompt (harness context included) with the usual separator.
func TestBuildSystemPromptForSessionWithFolder(t *testing.T) {
	cb, folder := newFolderTestBuilder(t)
	const sessionKey = "native:client-1:conv-1"

	base := cb.BuildSystemPromptForSession(sessionKey, "native")
	if strings.Contains(base, "## Selected Folder") {
		t.Fatal("precondition: base prompt should not contain the folder section")
	}

	var seenMu sync.Mutex
	var seen []string
	cb.SetFolderResolver(func(sk string) string {
		seenMu.Lock()
		seen = append(seen, sk)
		seenMu.Unlock()
		if sk == sessionKey {
			return folder
		}
		return ""
	})

	withFolder := cb.BuildSystemPromptForSessionWithFolder(sessionKey, "native")
	if !strings.HasPrefix(withFolder, base) {
		t.Error("folder section must be appended after the base prompt")
	}
	if !strings.Contains(withFolder, "\n\n---\n\n## Selected Folder") {
		t.Errorf("expected section separator before folder context; got:\n%s", withFolder[len(base)-40:])
	}
	if !strings.Contains(withFolder, "Folder: `"+folder+"`") {
		t.Error("folder path missing from prompt")
	}

	// The resolver must be asked with the *session key*, not the chat ID.
	seenMu.Lock()
	asked := strings.Join(seen, ",")
	seenMu.Unlock()
	if !strings.Contains(asked, sessionKey) {
		t.Errorf("resolver was never asked for %q (seen: %s)", sessionKey, asked)
	}

	// Unknown session key → resolver returns "" → identical to the base prompt.
	if got := cb.BuildSystemPromptForSessionWithFolder("telegram:42", "telegram"); got != cb.BuildSystemPromptForSession("telegram:42", "telegram") {
		t.Error("session without a folder must match the base prompt exactly")
	}
}

// TestBuildSystemPromptForSessionUnchanged guards the spec requirement that
// the pre-existing entry point keeps its behavior (existing tests rely on it).
func TestBuildSystemPromptForSessionUnchanged(t *testing.T) {
	cb, folder := newFolderTestBuilder(t)
	cb.SetFolderResolver(func(string) string { return folder })

	if strings.Contains(cb.BuildSystemPromptForSession("native:c:1", "native"), "## Selected Folder") {
		t.Error("BuildSystemPromptForSession must stay folder-free")
	}
}

// TestSetFolderResolverConcurrentReads exercises the dedicated mutex: prompt
// builders read the resolver while the loop may swap it.
func TestSetFolderResolverConcurrentReads(t *testing.T) {
	cb, folder := newFolderTestBuilder(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = cb.BuildSystemPromptForSessionWithFolder("native:client-1:conv", "native")
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			cb.SetFolderResolver(func(string) string { return folder })
			cb.SetFolderResolver(nil)
		}
	}()
	wg.Wait()
}
