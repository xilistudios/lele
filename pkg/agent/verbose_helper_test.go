// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"strings"
	"testing"
)

// TestFormatBasicToolMessage covers the formatBasicToolMessage function across
// every tool kind plus the default fallback path.
func TestFormatBasicToolMessage(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     map[string]interface{}
		want     string
	}{
		{
			name:     "exec",
			toolName: "exec",
			args:     map[string]interface{}{"command": "git push origin main"},
			want:     "🛠️ Exec: push git changes",
		},
		{
			name:     "read",
			toolName: "read",
			args:     map[string]interface{}{"path": "/tmp/file.txt"},
			want:     "🛠️ Read: file.txt (in /tmp)",
		},
		{
			name:     "read_file",
			toolName: "read_file",
			args:     map[string]interface{}{"path": "note.md"},
			want:     "🛠️ Read: note.md (in workspace)",
		},
		{
			name:     "write",
			toolName: "write",
			args:     map[string]interface{}{"path": "out.txt", "content": "line1\nline2"},
			want:     "🛠️ Write: out.txt (in workspace)\n→ line1",
		},
		{
			name:     "write_file",
			toolName: "write_file",
			args:     map[string]interface{}{"path": "a/b/out.txt", "content": "hello world"},
			want:     "🛠️ Write: out.txt (in a/b)\n→ hello world",
		},
		{
			name:     "edit",
			toolName: "edit",
			args:     map[string]interface{}{"path": "file.go", "content": "package main"},
			want:     "🛠️ Edit: file.go (in workspace)\n→ package main",
		},
		{
			name:     "smart_edit",
			toolName: "smart_edit",
			args:     map[string]interface{}{"path": "x.go", "content": "code"},
			want:     "🛠️ Edit: x.go (in workspace)\n→ code",
		},
		{
			name:     "patch",
			toolName: "patch",
			args:     map[string]interface{}{"path": "y.go"},
			want:     "🛠️ Patch: y.go (in workspace)",
		},
		{
			name:     "web_search",
			toolName: "web_search",
			args:     map[string]interface{}{"query": "latest news"},
			want:     "🛠️ Search: \"latest news\"",
		},
		{
			name:     "web_fetch",
			toolName: "web_fetch",
			args:     map[string]interface{}{"url": "https://example.com"},
			want:     "🛠️ Fetch: https://example.com",
		},
		{
			name:     "send_file",
			toolName: "send_file",
			args:     map[string]interface{}{"channel": "telegram", "chat_id": "123", "content": "hello"},
			want:     "🛠️ Send file to telegram:123: \"hello\"",
		},
		{
			name:     "spawn",
			toolName: "spawn",
			args:     map[string]interface{}{"task": "do the thing"},
			want:     "🛠️ Spawn: do the thing",
		},
		{
			name:     "list",
			toolName: "list",
			args:     map[string]interface{}{"path": "/some/dir"},
			want:     "🛠️ List: /some/dir",
		},
		{
			name:     "default-other-tool",
			toolName: "random_tool",
			args:     map[string]interface{}{"foo": "bar"},
			want:     "🛠️ Random_tool: {\"foo\":\"bar\"}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatBasicToolMessage(tt.toolName, tt.args)
			if !strings.Contains(got, tt.want) && got != tt.want {
				t.Errorf("formatBasicToolMessage(%s) = %q, want substring %q", tt.toolName, got, tt.want)
			}
		})
	}
}

// TestFormatBasicToolMessage_DefaultPreviewTruncated covers long JSON preview
// truncation in the default branch and that the result is length-limited.
func TestFormatBasicToolMessage_DefaultPreviewTruncated(t *testing.T) {
	longVal := strings.Repeat("x", 200)
	got := formatBasicToolMessage("some_tool", map[string]interface{}{"key": longVal})
	if len(got) > MaxBasicMessageSize {
		t.Errorf("result too long: %d > %d: %q", len(got), MaxBasicMessageSize, got)
	}
	if !strings.Contains(got, "...") {
		t.Errorf("expected truncation marker, got: %q", got)
	}
}

// TestFormatBasicToolMessage_ExecLongCommand covers exec with truncated command/cwd.
func TestFormatBasicToolMessage_ExecLongCommand(t *testing.T) {
	got := formatBasicToolMessage("exec", map[string]interface{}{
		"command": "echo " + strings.Repeat("a", 250),
		"cwd":     "/" + strings.Repeat("d", 60),
	})
	if len(got) > MaxBasicMessageSize {
		t.Errorf("result too long: %d > %d", len(got), MaxBasicMessageSize)
	}
}

// TestFormatBasicToolMessage_ExecNoCommand covers exec without a command.
func TestFormatBasicToolMessage_ExecNoCommand(t *testing.T) {
	got := formatBasicToolMessage("exec", map[string]interface{}{})
	if got != "🛠️ Exec: [no command]" {
		t.Errorf("got %q, want %q", got, "🛠️ Exec: [no command]")
	}
}

// TestFormatBasicToolMessage_NoPathFileOp covers file ops with no path.
func TestFormatBasicToolMessage_NoPathFileOp(t *testing.T) {
	for _, tool := range []string{"read", "write", "edit", "append", "patch"} {
		got := formatBasicToolMessage(tool, map[string]interface{}{"content": "x"})
		if !strings.Contains(got, "[no path]") {
			t.Errorf("tool %s: got %q, want [no path]", tool, got)
		}
	}
}

// TestFormatBasicToolMessage_EmptyArgs covers missing args for web/search/fetch tools.
func TestFormatBasicToolMessage_EmptyArgs(t *testing.T) {
	cases := []struct {
		tool string
		want string
	}{
		{"web_search", "🛠️ Search: [no query]"},
		{"web_fetch", "🛠️ Fetch: [no url]"},
		{"spawn", "🛠️ Spawn: [no task]"},
		{"send_file", "🛠️ Send file to unknown"},
		{"list", "🛠️ List: current"},
	}

	for _, c := range cases {
		got := formatBasicToolMessage(c.tool, map[string]interface{}{})
		if got != c.want {
			t.Errorf("tool %s: got %q, want %q", c.tool, got, c.want)
		}
	}
}

// TestFormatBasicWebSearchLongQuery covers query truncation in web_search.
func TestFormatBasicWebSearchLongQuery(t *testing.T) {
	got := formatBasicToolMessage("web_search", map[string]interface{}{
		"query": strings.Repeat("q", 150),
	})
	if !strings.Contains(got, "...") {
		t.Errorf("expected truncation, got %q", got)
	}
}

// TestFormatBasicWebFetchLongURL covers URL truncation in web_fetch.
func TestFormatBasicWebFetchLongURL(t *testing.T) {
	got := formatBasicToolMessage("web_fetch", map[string]interface{}{
		"url": "https://example.com/" + strings.Repeat("p", 100),
	})
	if !strings.Contains(got, "...") {
		t.Errorf("expected truncation, got %q", got)
	}
}

// TestFormatBasicSpawnLongTask covers task truncation in spawn.
func TestFormatBasicSpawnLongTask(t *testing.T) {
	got := formatBasicToolMessage("spawn", map[string]interface{}{
		"task": strings.Repeat("t", 80),
	})
	if !strings.Contains(got, "...") {
		t.Errorf("expected truncation, got %q", got)
	}
}

// TestFormatBasicListDirLongPath covers path truncation in list_dir.
func TestFormatBasicListDirLongPath(t *testing.T) {
	got := formatBasicToolMessage("list_dir", map[string]interface{}{
		"path": "/" + strings.Repeat("d", 120),
	})
	if !strings.Contains(got, "...") {
		t.Errorf("expected truncation, got %q", got)
	}
}

// TestFormatBasicExec_Sudo covers sudo handling in extractCommandDescription.
func TestExtractCommandDescription(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{"git commit -m x", "commit git changes"},
		{"git push origin main", "push git changes"},
		{"git pull", "pull git changes"},
		{"git status", "check git status"},
		{"git add .", "stage git changes"},
		{"git checkout main", "switch git branch"},
		{"git log", "view git history"},
		{"git diff", "view git diff"},
		{"git clone url", "clone repository"},
		{"go build ./...", "build Go project"},
		{"go run main.go", "run Go program"},
		{"go test ./...", "run Go tests"},
		{"go mod tidy", "manage Go modules"},
		{"npm install x", "install npm packages"},
		{"npm run build", "run npm script"},
		{"npm run build && make", "run npm script"},
		{"npm build", "build npm project"},
		{"make all", "run make"},
		{"docker build -t x .", "build Docker image"},
		{"docker run x", "run Docker container"},
		{"docker compose up", "run Docker compose"},
		{"cargo build", "build Go project"},
		{"cargo run", "run Go program"},
		{"mkdir -p /tmp/x", "create directory"},
		{"rm -rf /tmp/x", "remove directory"},
		{"rm /tmp/x", "remove files"},
		{"cp -r a b", "copy directory"},
		{"cp a b", "copy files"},
		{"mv a b", "move files"},
		{"cat file", "display file"},
		{"head file", "view file start"},
		{"tail file", "view file end"},
		{"less file", "view file"},
		{"ls -la", "list directory"},
		{"find . -name x", "find files"},
		{"grep foo bar", "search in files"},
		{"chmod 755 x", "change permissions"},
		{"chown root x", "change ownership"},
		{"tar -czf x.tar x", "archive files"},
		{"zip -r x.zip x", "archive files"},
		{"gzip x", "archive files"},
		{"ps aux", "list processes"},
		{"kill -9 123", "kill process"},
		{"top", "monitor processes"},
		{"htop", "monitor processes"},
		{"systemctl status nginx", "manage service"},
		{"journalctl -f", "view logs"},
		{"curl http://example.com", "HTTP request"},
		{"wget http://example.com", "download file"},
		{"ping 8.8.8.8", "ping host"},
		{"ssh user@host", "SSH connection"},
		{"scp a b", "copy files"},
		{"sudo apt update", "apt (with sudo)"},
		{"cd /tmp && ls", "ls command"},
		{"custom_tool arg", "custom_tool command"},
		{"", "execute command"},
		{"   ", "execute command"},
	}

	for _, tt := range tests {
		got := extractCommandDescription(tt.cmd)
		if got != tt.want {
			t.Errorf("extractCommandDescription(%q) = %q, want %q", tt.cmd, got, tt.want)
		}
	}
}

// TestFormatBasicSendFileNoContent covers the no-content send_file branch.
func TestFormatBasicSendFileNoContent(t *testing.T) {
	got := formatBasicSendFile(map[string]interface{}{
		"channel": "telegram",
		"chat_id": "999",
	})
	if got != "🛠️ Send file to telegram:999" {
		t.Errorf("got %q", got)
	}
}

// TestFormatBasicSendFileUnknownChannel covers default channel fallback.
func TestFormatBasicSendFileUnknownChannel(t *testing.T) {
	got := formatBasicSendFile(map[string]interface{}{})
	if got != "🛠️ Send file to unknown" {
		t.Errorf("got %q", got)
	}
}
