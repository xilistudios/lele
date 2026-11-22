package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

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

	other := cb.BuildMessages([]providers.Message{}, "", "hello", nil, "native", "native:client-1", "native:client-1:other", "")
	if strings.Contains(other[0].Content, "## Selected Folder") {
		t.Error("folder context leaked into a session without a selected folder")
	}
}

func TestBuildMessagesWithoutFolderResolver(t *testing.T) {
	cb, _ := newFolderTestBuilder(t)

	messages := cb.BuildMessages([]providers.Message{}, "", "hello", nil, "native", "native:client-1", "native:client-1:conv", "")
	if strings.Contains(messages[0].Content, "## Selected Folder") {
		t.Error("nil resolver must not inject folder context")
	}

	cb.SetFolderResolver(func(string) string { return "" })
	messages = cb.BuildMessages([]providers.Message{}, "", "hello", nil, "native", "native:client-1", "native:client-1:conv", "")
	if strings.Contains(messages[0].Content, "## Selected Folder") {
		t.Error("empty folder must not inject folder context")
	}
}

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

	seenMu.Lock()
	asked := strings.Join(seen, ",")
	seenMu.Unlock()
	if !strings.Contains(asked, sessionKey) {
		t.Errorf("resolver was never asked for %q (seen: %s)", sessionKey, asked)
	}

	if got := cb.BuildSystemPromptForSessionWithFolder("telegram:42", "telegram"); got != cb.BuildSystemPromptForSession("telegram:42", "telegram") {
		t.Error("session without a folder must match the base prompt exactly")
	}
}

func TestBuildSystemPromptForSessionUnchanged(t *testing.T) {
	cb, folder := newFolderTestBuilder(t)
	cb.SetFolderResolver(func(string) string { return folder })

	if strings.Contains(cb.BuildSystemPromptForSession("native:c:1", "native"), "## Selected Folder") {
		t.Error("BuildSystemPromptForSession must stay folder-free")
	}
}

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
