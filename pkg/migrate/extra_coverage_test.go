package migrate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// captureStdout redirects os.Stdout to a pipe and drains it, suppressing
// console output from functions like PrintPlan/PrintSummary/Execute.
func captureStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	buf := &bytes.Buffer{}
	done := make(chan struct{})
	go func() {
		_, _ = buf.ReadFrom(r)
		close(done)
	}()
	t.Cleanup(func() {
		w.Close()
		os.Stdout = old
		<-done
	})
	return buf
}

func TestConfirm(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"lowercase y", "y\n", true},
		{"uppercase Y", "Y\n", true},
		{"spaces around y", "  y  \n", true},
		{"no", "n\n", false},
		{"empty", "\n", false},
		{"other", "maybe\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldStdin := os.Stdin
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			os.Stdin = r
			w.WriteString(tt.input)
			w.Close()

			got := Confirm()

			os.Stdin = oldStdin
			r.Close()
			if got != tt.want {
				t.Errorf("Confirm() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunAbortsOnConfirmNo(t *testing.T) {
	captureStdout(t)

	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	w.WriteString("n\n")
	w.Close()
	defer func() {
		os.Stdin = oldStdin
		r.Close()
	}()

	openclawHome := t.TempDir()
	picoClawHome := t.TempDir()

	configData := map[string]interface{}{
		"providers": map[string]interface{}{
			"anthropic": map[string]interface{}{"apiKey": "sk-key"},
		},
	}
	data, _ := json.Marshal(configData)
	os.WriteFile(filepath.Join(openclawHome, "openclaw.json"), data, 0644)

	opts := Options{OpenClawHome: openclawHome, LeleHome: picoClawHome}
	result, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ConfigMigrated {
		t.Error("config should not be migrated after abort")
	}
	if _, statErr := os.Stat(filepath.Join(picoClawHome, "config.json")); !os.IsNotExist(statErr) {
		t.Error("config.json should not exist after abort")
	}
}

func TestPrintSummary(t *testing.T) {
	captureStdout(t)

	t.Run("all positive", func(t *testing.T) {
		PrintSummary(&Result{FilesCopied: 3, ConfigMigrated: true, FilesSkipped: 2})
	})
	t.Run("no actions taken", func(t *testing.T) {
		PrintSummary(&Result{})
	})
	t.Run("with errors", func(t *testing.T) {
		PrintSummary(&Result{FilesCopied: 1, Errors: []error{os.ErrNotExist}})
	})
}

func TestResolveOpenClawHome(t *testing.T) {
	t.Run("override wins", func(t *testing.T) {
		got, err := resolveOpenClawHome("/custom/path")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/custom/path" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("override with tilde expanded", func(t *testing.T) {
		got, err := resolveOpenClawHome("~/custom")
		if err != nil {
			t.Fatal(err)
		}
		home, _ := os.UserHomeDir()
		if got != filepath.Join(home, "custom") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("env var OPENCLAW_HOME", func(t *testing.T) {
		os.Setenv("OPENCLAW_HOME", "/env/home")
		defer os.Unsetenv("OPENCLAW_HOME")
		got, err := resolveOpenClawHome("")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/env/home" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("default falls back to ~/.openclaw", func(t *testing.T) {
		os.Unsetenv("OPENCLAW_HOME")
		got, err := resolveOpenClawHome("")
		if err != nil {
			t.Fatal(err)
		}
		home, _ := os.UserHomeDir()
		if got != filepath.Join(home, ".openclaw") {
			t.Errorf("got %q", got)
		}
	})
}

func TestResolveLeleHome(t *testing.T) {
	t.Run("override wins", func(t *testing.T) {
		got, err := resolveLeleHome("/custom")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/custom" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("LELE_CONFIG_DIR", func(t *testing.T) {
		os.Setenv("LELE_CONFIG_DIR", "/cfg/home")
		os.Unsetenv("LELE_HOME")
		defer os.Unsetenv("LELE_CONFIG_DIR")
		got, err := resolveLeleHome("")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/cfg/home" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("LELE_HOME", func(t *testing.T) {
		os.Unsetenv("LELE_CONFIG_DIR")
		os.Setenv("LELE_HOME", "/lele/home")
		defer os.Unsetenv("LELE_HOME")
		got, err := resolveLeleHome("")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/lele/home" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("default falls back to ~/.lele", func(t *testing.T) {
		os.Unsetenv("LELE_CONFIG_DIR")
		os.Unsetenv("LELE_HOME")
		got, err := resolveLeleHome("")
		if err != nil {
			t.Fatal(err)
		}
		home, _ := os.UserHomeDir()
		if got != filepath.Join(home, ".lele") {
			t.Errorf("got %q", got)
		}
	})
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"bare tilde", "~", home},
		{"tilde slash path", "~/foo", filepath.Join(home, "foo")},
		{"absolute unchanged", "/abs/path", "/abs/path"},
		{"relative unchanged", "rel/path", "rel/path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandHome(tt.in)
			if got != tt.want {
				t.Errorf("expandHome(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRunConfigOnlyConfigNotFound(t *testing.T) {
	captureStdout(t)
	opts := Options{
		Force:        true,
		ConfigOnly:   true,
		OpenClawHome: t.TempDir(), // no config present
		LeleHome:     t.TempDir(),
	}
	_, err := Run(opts)
	if err == nil {
		t.Fatal("expected error when config-only and no config found")
	}
}

func TestRunRefreshSetsWorkspaceOnly(t *testing.T) {
	captureStdout(t)
	openclawHome := t.TempDir()
	picoClawHome := t.TempDir()

	wsDir := filepath.Join(openclawHome, "workspace")
	os.MkdirAll(wsDir, 0755)
	os.WriteFile(filepath.Join(wsDir, "SOUL.md"), []byte("# Soul"), 0644)

	configData := map[string]interface{}{
		"providers": map[string]interface{}{"anthropic": map[string]interface{}{"apiKey": "sk-key"}},
	}
	data, _ := json.Marshal(configData)
	os.WriteFile(filepath.Join(openclawHome, "openclaw.json"), data, 0644)

	opts := Options{Force: true, Refresh: true, OpenClawHome: openclawHome, LeleHome: picoClawHome}
	result, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ConfigMigrated {
		t.Error("Refresh implies workspace-only; config should not migrate")
	}
	if _, statErr := os.Stat(filepath.Join(picoClawHome, "workspace", "SOUL.md")); statErr != nil {
		t.Errorf("SOUL.md should be copied: %v", statErr)
	}
}

func TestRunWorkspaceSkippedWhenMissing(t *testing.T) {
	captureStdout(t)
	openclawHome := t.TempDir()
	configData := map[string]interface{}{
		"providers": map[string]interface{}{"anthropic": map[string]interface{}{"apiKey": "sk-key"}},
	}
	data, _ := json.Marshal(configData)
	os.WriteFile(filepath.Join(openclawHome, "openclaw.json"), data, 0644)

	opts := Options{Force: true, OpenClawHome: openclawHome, LeleHome: t.TempDir()}
	result, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "workspace directory not found") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected workspace-not-found warning, got %v", result.Warnings)
	}
}

func TestRunConfigSkippedWhenMissingNonConfigOnly(t *testing.T) {
	captureStdout(t)
	openclawHome := t.TempDir()
	wsDir := filepath.Join(openclawHome, "workspace")
	os.MkdirAll(wsDir, 0755)
	os.WriteFile(filepath.Join(wsDir, "SOUL.md"), []byte("# Soul"), 0644)

	opts := Options{Force: true, OpenClawHome: openclawHome, LeleHome: t.TempDir()}
	result, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "Config migration skipped") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected config-skipped warning, got %v", result.Warnings)
	}
}

func TestPlanWorkspaceNotFound(t *testing.T) {
	openclawHome := t.TempDir() // no workspace dir, no config
	actions, warnings, err := Plan(Options{}, openclawHome, t.TempDir())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "workspace directory not found") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected workspace warning, got %v", warnings)
	}
	if len(actions) != 0 {
		t.Errorf("expected no actions, got %d", len(actions))
	}
}

func TestExecuteAllActionTypes(t *testing.T) {
	captureStdout(t)
	openclawHome := t.TempDir()
	picoClawHome := t.TempDir()

	src := filepath.Join(openclawHome, "src.md")
	os.WriteFile(src, []byte("hello"), 0644)

	actions := []Action{
		{Type: ActionCreateDir, Destination: filepath.Join(picoClawHome, "sub", "dir")},
		{Type: ActionCopy, Source: src, Destination: filepath.Join(picoClawHome, "dst.md")},
		{Type: ActionSkip, Source: src, Destination: filepath.Join(picoClawHome, "skip.md"), Description: "skip it"},
		{Type: ActionSkip, Source: src, Destination: filepath.Join(picoClawHome, "skip2.md")},
	}

	result := Execute(actions, openclawHome, picoClawHome)
	if result.DirsCreated != 1 || result.FilesCopied != 1 || result.FilesSkipped != 2 {
		t.Errorf("got Dirs=%d Copies=%d Skips=%d", result.DirsCreated, result.FilesCopied, result.FilesSkipped)
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected no errors, got %v", result.Errors)
	}
	if _, err := os.Stat(filepath.Join(picoClawHome, "sub", "dir")); err != nil {
		t.Errorf("dir should exist: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(picoClawHome, "dst.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("dst content = %q", string(data))
	}
}

func TestExecuteCopyFailure(t *testing.T) {
	captureStdout(t)
	actions := []Action{
		{Type: ActionCopy, Source: filepath.Join(t.TempDir(), "missing.md"), Destination: filepath.Join(t.TempDir(), "dst.md")},
	}
	result := Execute(actions, t.TempDir(), t.TempDir())
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %v", result.Errors)
	}
	if result.FilesCopied != 0 {
		t.Errorf("FilesCopied = %d, want 0", result.FilesCopied)
	}
}

func TestExecuteConfigMigrationFailure(t *testing.T) {
	captureStdout(t)
	actions := []Action{
		{Type: ActionConvertConfig, Source: filepath.Join(t.TempDir(), "nope.json"), Destination: filepath.Join(t.TempDir(), "config.json")},
	}
	result := Execute(actions, t.TempDir(), t.TempDir())
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %v", result.Errors)
	}
	if result.ConfigMigrated {
		t.Error("ConfigMigrated should be false on failure")
	}
}

func TestExecuteCreateDirFailure(t *testing.T) {
	captureStdout(t)
	picoClawHome := t.TempDir()
	blocker := filepath.Join(picoClawHome, "blocker")
	os.WriteFile(blocker, []byte("x"), 0644)

	actions := []Action{
		{Type: ActionCreateDir, Destination: filepath.Join(blocker, "child")},
		{Type: ActionCopy, Source: filepath.Join(t.TempDir(), "a.md"), Destination: filepath.Join(blocker, "child", "a.md")},
	}
	result := Execute(actions, t.TempDir(), picoClawHome)
	if len(result.Errors) != 2 {
		t.Errorf("expected 2 errors, got %v", result.Errors)
	}
	if result.DirsCreated != 0 {
		t.Errorf("DirsCreated = %d, want 0", result.DirsCreated)
	}
}

func TestPrintPlanVariousActions(t *testing.T) {
	captureStdout(t)

	actions := []Action{
		{Type: ActionConvertConfig, Source: "/s/config.json", Destination: "/d/config.json"},
		{Type: ActionCopy, Source: "/s/a.md", Destination: "/d/a.md", Description: "copy file"},
		{Type: ActionCopy, Source: "/s/b.md", Destination: "/d/b.md", Description: "special"},
		{Type: ActionSkip, Source: "/s/c.md", Destination: "/d/c.md", Description: "source file not found"},
		{Type: ActionSkip, Source: "/s/d.md", Destination: "/d/d.md"},
		{Type: ActionCreateDir, Destination: "/d/sub"},
	}
	PrintPlan(actions, []string{"warning one", "warning two"})

	PrintPlan([]Action{{Type: ActionCopy, Source: "/s/a.md", Destination: "/d/a.md"}}, nil)
	PrintPlan(nil, nil)
}

func TestRelPathFallback(t *testing.T) {
	got := relPath("/a/b/file.md", "other/path")
	if got != "file.md" {
		t.Errorf("relPath = %q, want file.md", got)
	}
}

func TestLoadOpenClawConfigErrors(t *testing.T) {
	t.Run("file not found", func(t *testing.T) {
		_, err := LoadOpenClawConfig(filepath.Join(t.TempDir(), "missing.json"))
		if err == nil {
			t.Fatal("expected error for missing file")
		}
		if !strings.Contains(err.Error(), "reading OpenClaw config") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "bad.json")
		os.WriteFile(p, []byte("{not json"), 0644)
		_, err := LoadOpenClawConfig(p)
		if err == nil {
			t.Fatal("expected parse error")
		}
		if !strings.Contains(err.Error(), "parsing OpenClaw config") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("non-map top level json", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "array.json")
		os.WriteFile(p, []byte(`[1, 2, 3]`), 0644)
		_, err := LoadOpenClawConfig(p)
		if err == nil {
			t.Fatal("expected error for non-map top level")
		}
	})
}

func TestExecuteConfigMigrationSuccess(t *testing.T) {
	openclawHome := t.TempDir()
	picoClawHome := t.TempDir()

	src := filepath.Join(openclawHome, "openclaw.json")
	os.WriteFile(src, []byte(`{"providers":{"anthropic":{"apiKey":"sk-k","apiBase":"b"}}}`), 0644)
	dst := filepath.Join(picoClawHome, "config.json")

	if err := executeConfigMigration(src, dst, picoClawHome); err != nil {
		t.Fatalf("executeConfigMigration: %v", err)
	}
	cfg, err := config.LoadConfig(dst)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers.Anthropic.APIKey != "sk-k" {
		t.Errorf("Anthropic key = %q", cfg.Providers.Anthropic.APIKey)
	}
}

func TestExecuteConfigMigrationMergesExisting(t *testing.T) {
	picoClawHome := t.TempDir()
	openclawHome := t.TempDir()

	src := filepath.Join(openclawHome, "openclaw.json")
	os.WriteFile(src, []byte(`{"providers":{"anthropic":{"apiKey":"incoming","apiBase":"b"}}}`), 0644)
	dst := filepath.Join(picoClawHome, "config.json")
	os.WriteFile(dst, []byte(`{"providers":{"anthropic":{"api_key":"existing"}}}`), 0600)

	if err := executeConfigMigration(src, dst, picoClawHome); err != nil {
		t.Fatalf("executeConfigMigration: %v", err)
	}
	cfg, err := config.LoadConfig(dst)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers.Anthropic.APIKey != "existing" {
		t.Errorf("existing key should be preserved, got %q", cfg.Providers.Anthropic.APIKey)
	}
}

func TestExecuteConfigMigrationMissingSource(t *testing.T) {
	err := executeConfigMigration(filepath.Join(t.TempDir(), "missing.json"), filepath.Join(t.TempDir(), "config.json"), t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing source")
	}
}

func TestExecuteConfigMigrationInvalidSource(t *testing.T) {
	src := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(src, []byte("{bad"), 0644)
	err := executeConfigMigration(src, filepath.Join(t.TempDir(), "config.json"), t.TempDir())
	if err == nil {
		t.Fatal("expected error for invalid source")
	}
}

func TestExecuteConfigMigrationInvalidExisting(t *testing.T) {
	picoClawHome := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.json")
	os.WriteFile(src, []byte(`{"providers":{"anthropic":{"apiKey":"k"}}}`), 0644)
	dst := filepath.Join(picoClawHome, "config.json")
	os.WriteFile(dst, []byte("{invalid json"), 0600)

	err := executeConfigMigration(src, dst, picoClawHome)
	if err == nil {
		t.Fatal("expected error loading existing config")
	}
}

func TestConvertConfigChannels(t *testing.T) {
	t.Run("whatsapp", func(t *testing.T) {
		data := map[string]interface{}{
			"channels": map[string]interface{}{
				"whatsapp": map[string]interface{}{"enabled": true, "bridge_url": "ws://x", "allow_from": []interface{}{"a"}},
			},
		}
		cfg, _, err := ConvertConfig(data)
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.Channels.WhatsApp.Enabled || cfg.Channels.WhatsApp.BridgeURL != "ws://x" {
			t.Errorf("whatsapp not migrated: %+v", cfg.Channels.WhatsApp)
		}
	})
	t.Run("feishu", func(t *testing.T) {
		data := map[string]interface{}{
			"channels": map[string]interface{}{
				"feishu": map[string]interface{}{"enabled": true, "app_id": "id", "app_secret": "sec", "encrypt_key": "ek", "verification_token": "vt"},
			},
		}
		cfg, _, err := ConvertConfig(data)
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.Channels.Feishu.Enabled || cfg.Channels.Feishu.AppID != "id" || cfg.Channels.Feishu.AppSecret != "sec" ||
			cfg.Channels.Feishu.EncryptKey != "ek" || cfg.Channels.Feishu.VerificationToken != "vt" {
			t.Errorf("feishu not migrated: %+v", cfg.Channels.Feishu)
		}
	})
	t.Run("qq", func(t *testing.T) {
		data := map[string]interface{}{
			"channels": map[string]interface{}{"qq": map[string]interface{}{"enabled": true, "app_id": "id", "app_secret": "sec"}},
		}
		cfg, _, err := ConvertConfig(data)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Channels.QQ.AppID != "id" || cfg.Channels.QQ.AppSecret != "sec" {
			t.Errorf("qq not migrated: %+v", cfg.Channels.QQ)
		}
	})
	t.Run("dingtalk", func(t *testing.T) {
		data := map[string]interface{}{
			"channels": map[string]interface{}{"dingtalk": map[string]interface{}{"enabled": true, "client_id": "id", "client_secret": "sec"}},
		}
		cfg, _, err := ConvertConfig(data)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Channels.DingTalk.ClientID != "id" || cfg.Channels.DingTalk.ClientSecret != "sec" {
			t.Errorf("dingtalk not migrated: %+v", cfg.Channels.DingTalk)
		}
	})
	t.Run("maixcam", func(t *testing.T) {
		data := map[string]interface{}{
			"channels": map[string]interface{}{"maixcam": map[string]interface{}{"enabled": true, "host": "h", "port": float64(1234)}},
		}
		cfg, _, err := ConvertConfig(data)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Channels.MaixCam.Host != "h" || cfg.Channels.MaixCam.Port != 1234 {
			t.Errorf("maixcam not migrated: %+v", cfg.Channels.MaixCam)
		}
	})
}

func TestConvertConfigUnsupportedChannelValue(t *testing.T) {
	data := map[string]interface{}{
		"channels": map[string]interface{}{"telegram": "not-a-map"},
	}
	cfg, _, err := ConvertConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Channels.Telegram.Enabled {
		t.Error("telegram should not be enabled for non-map value")
	}
}

func TestConvertConfigProviderNonMap(t *testing.T) {
	data := map[string]interface{}{
		"providers": map[string]interface{}{"anthropic": "not-a-map"},
	}
	cfg, _, err := ConvertConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers.Anthropic.APIKey != "" {
		t.Error("should skip non-map provider")
	}
}

func TestConvertConfigProvidersAll(t *testing.T) {
	data := map[string]interface{}{
		"providers": map[string]interface{}{
			"openai":     map[string]interface{}{"api_key": "k1", "api_base": "b1", "web_search": false},
			"zhipu":      map[string]interface{}{"api_key": "k2"},
			"vllm":       map[string]interface{}{"api_key": "k3", "api_base": "b3"},
			"gemini":     map[string]interface{}{"api_key": "k4"},
			"anthropic":  map[string]interface{}{"api_key": "k5"},
			"groq":       map[string]interface{}{"api_key": "k6"},
			"openrouter": map[string]interface{}{"api_key": "k7"},
		},
	}
	cfg, warnings, err := ConvertConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
	if cfg.Providers.OpenAI.WebSearch {
		t.Error("OpenAI WebSearch should be false (was explicitly set)")
	}
	if cfg.Providers.Zhipu.APIKey != "k2" || cfg.Providers.VLLM.APIKey != "k3" ||
		cfg.Providers.Gemini.APIKey != "k4" || cfg.Providers.Anthropic.APIKey != "k5" ||
		cfg.Providers.Groq.APIKey != "k6" || cfg.Providers.OpenRouter.APIKey != "k7" {
		t.Errorf("provider keys not fully migrated")
	}
}

func TestConvertConfigGatewayAndTools(t *testing.T) {
	data := map[string]interface{}{
		"gateway": map[string]interface{}{"host": "1.2.3.4", "port": float64(9000)},
		"tools": map[string]interface{}{
			"web": map[string]interface{}{
				"search": map[string]interface{}{
					"api_key":     "brave-key",
					"max_results": float64(7),
				},
			},
		},
	}
	cfg, _, err := ConvertConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Gateway.Host != "1.2.3.4" || cfg.Gateway.Port != 9000 {
		t.Errorf("gateway not migrated: %+v", cfg.Gateway)
	}
	if cfg.Tools.Web.Brave.APIKey != "brave-key" || !cfg.Tools.Web.Brave.Enabled {
		t.Errorf("brave not migrated: %+v", cfg.Tools.Web.Brave)
	}
	if cfg.Tools.Web.Brave.MaxResults != 7 || cfg.Tools.Web.DuckDuckGo.MaxResults != 7 {
		t.Errorf("max_results not migrated: brave=%d ddg=%d", cfg.Tools.Web.Brave.MaxResults, cfg.Tools.Web.DuckDuckGo.MaxResults)
	}
}

func TestMergeConfigAllProviders(t *testing.T) {
	t.Run("fills each provider when empty", func(t *testing.T) {
		existing := config.DefaultConfig()
		incoming := config.DefaultConfig()
		incoming.Providers.Zhipu.APIKey = "z"
		incoming.Providers.VLLM.APIBase = "v"
		incoming.Providers.Groq.APIKey = "g"
		incoming.Providers.Gemini.APIKey = "gm"
		incoming.Providers.OpenAI.APIKey = "o"
		incoming.Providers.OpenRouter.APIKey = "or"

		result := MergeConfig(existing, incoming)
		if result.Providers.Zhipu.APIKey != "z" || result.Providers.Groq.APIKey != "g" ||
			result.Providers.Gemini.APIKey != "gm" || result.Providers.OpenAI.APIKey != "o" ||
			result.Providers.OpenRouter.APIKey != "or" {
			t.Errorf("providers not filled: %+v", result.Providers)
		}
		if result.Providers.VLLM.APIBase != "v" {
			t.Errorf("VLLM APIBase not filled, got %q", result.Providers.VLLM.APIBase)
		}
	})
	t.Run("vllm requires both key and base empty to fill", func(t *testing.T) {
		existing := config.DefaultConfig()
		existing.Providers.VLLM.APIBase = "existing-base"
		incoming := config.DefaultConfig()
		incoming.Providers.VLLM.APIBase = "incoming"
		result := MergeConfig(existing, incoming)
		if result.Providers.VLLM.APIBase != "existing-base" {
			t.Errorf("VLLM base should be preserved, got %q", result.Providers.VLLM.APIBase)
		}
	})
	t.Run("fills channels", func(t *testing.T) {
		existing := config.DefaultConfig()
		incoming := config.DefaultConfig()
		incoming.Channels.Discord.Enabled = true
		incoming.Channels.Feishu.Enabled = true
		incoming.Channels.QQ.Enabled = true
		incoming.Channels.DingTalk.Enabled = true
		incoming.Channels.MaixCam.Enabled = true
		incoming.Channels.WhatsApp.Enabled = true

		result := MergeConfig(existing, incoming)
		if !result.Channels.Discord.Enabled || !result.Channels.Feishu.Enabled || !result.Channels.QQ.Enabled ||
			!result.Channels.DingTalk.Enabled || !result.Channels.MaixCam.Enabled || !result.Channels.WhatsApp.Enabled {
			t.Error("not all channels filled")
		}
	})
	t.Run("does not fill already-enabled channels", func(t *testing.T) {
		existing := config.DefaultConfig()
		existing.Channels.Feishu.Enabled = true
		existing.Channels.Feishu.AppID = "existing"
		incoming := config.DefaultConfig()
		incoming.Channels.Feishu.Enabled = true
		incoming.Channels.Feishu.AppID = "incoming"

		result := MergeConfig(existing, incoming)
		if result.Channels.Feishu.AppID != "existing" {
			t.Errorf("Feishu AppID should be preserved, got %q", result.Channels.Feishu.AppID)
		}
	})
	t.Run("brave tool fill", func(t *testing.T) {
		existing := config.DefaultConfig()
		incoming := config.DefaultConfig()
		incoming.Tools.Web.Brave.APIKey = "bk"
		result := MergeConfig(existing, incoming)
		if result.Tools.Web.Brave.APIKey != "bk" {
			t.Errorf("Brave APIKey not filled, got %q", result.Tools.Web.Brave.APIKey)
		}
	})
	t.Run("brave preserved when set", func(t *testing.T) {
		existing := config.DefaultConfig()
		existing.Tools.Web.Brave.APIKey = "existing"
		incoming := config.DefaultConfig()
		incoming.Tools.Web.Brave.APIKey = "incoming"
		result := MergeConfig(existing, incoming)
		if result.Tools.Web.Brave.APIKey != "existing" {
			t.Errorf("Brave APIKey should be preserved, got %q", result.Tools.Web.Brave.APIKey)
		}
	})
}

func TestHelpers(t *testing.T) {
	t.Run("getBoolOrDefault", func(t *testing.T) {
		m := map[string]interface{}{"web_search": true}
		if !getBoolOrDefault(m, "web_search", false) {
			t.Error("expected true from getBoolOrDefault")
		}
		if !getBoolOrDefault(map[string]interface{}{}, "absent", true) {
			t.Error("expected default true when absent")
		}
	})
	t.Run("getStringSlice non-slice", func(t *testing.T) {
		m := map[string]interface{}{"allow_from": "not-a-slice"}
		if got := getStringSlice(m, "allow_from"); len(got) != 0 {
			t.Errorf("expected empty slice, got %v", got)
		}
	})
	t.Run("getStringSlice mixed types", func(t *testing.T) {
		m := map[string]interface{}{"allow_from": []interface{}{"a", float64(5), "b"}}
		got := getStringSlice(m, "allow_from")
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("got %v", got)
		}
	})
	t.Run("getStringSlice absent", func(t *testing.T) {
		if got := getStringSlice(map[string]interface{}{}, "absent"); len(got) != 0 {
			t.Errorf("expected empty, got %v", got)
		}
	})
	t.Run("getMap nonexistent", func(t *testing.T) {
		if _, ok := getMap(map[string]interface{}{}, "x"); ok {
			t.Error("expected false")
		}
	})
	t.Run("getMap wrong type", func(t *testing.T) {
		if _, ok := getMap(map[string]interface{}{"x": "str"}, "x"); ok {
			t.Error("expected false for non-map")
		}
	})
}

func TestCamelToSnakeDigitBeforeUpper(t *testing.T) {
	got := camelToSnake("a1B")
	if got != "a1_b" {
		t.Errorf("camelToSnake(\"a1B\") = %q, want %q", got, "a1_b")
	}
}

func TestGetFloatWrongType(t *testing.T) {
	m := map[string]interface{}{"port": "not-a-float"}
	if _, ok := getFloat(m, "port"); ok {
		t.Error("expected false for non-float value")
	}
	if _, ok := getFloat(m, "absent"); ok {
		t.Error("expected false for absent key")
	}
}

func TestCamelToSnakeConsecutiveCapsBeforeLower(t *testing.T) {
	// "HTTPServer": 'S' is upper, prev 'P' is upper, next 'e' is lower -> underscore.
	got := camelToSnake("HTTPServer")
	if got != "http_server" {
		t.Errorf("camelToSnake(\"HTTPServer\") = %q, want %q", got, "http_server")
	}
}

func TestCopyFileErrorOpenDst(t *testing.T) {
	// src exists but dst points to a directory -> os.OpenFile fails.
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.md")
	os.WriteFile(src, []byte("x"), 0644)
	if err := copyFile(src, tmp); err == nil {
		t.Fatal("expected error when destination is a directory")
	}
}
