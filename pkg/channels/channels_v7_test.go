package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/skills"
	"github.com/xilistudios/lele/pkg/update"
)

// ---------------------------------------------------------------------------
// rest_system.go — skill installer error branches (no real network)
// ---------------------------------------------------------------------------

func TestSkillInstall_NetworkError(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.skillInstaller = skills.NewSkillInstaller(t.TempDir())

	// Valid body but the installer tries an HTTP fetch that will fail offline.
	rec := httptest.NewRecorder()
	httpReq := authenticatedRequest(t, ts, "/api/v1/skills")
	httpReq.Method = http.MethodPost
	httpReq.Body = mkBody(`{"url":"owner/repo"}`)
	ts.channel.handleSkillInstall(rec, httpReq)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("install offline = %d, want 500", rec.Code)
	}
}

func TestSkillInstall_AlreadyExists(t *testing.T) {
	ts := newNativeTestServer(t)
	dir := t.TempDir()
	ts.channel.skillInstaller = skills.NewSkillInstaller(dir)
	// Pre-create the skill dir so installer reports "already exists".
	if err := os.MkdirAll(filepath.Join(dir, "skills", "myrepo"), 0755); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	httpReq := authenticatedRequest(t, ts, "/api/v1/skills")
	httpReq.Method = http.MethodPost
	httpReq.Body = mkBody(`{"url":"owner/myrepo"}`)
	ts.channel.handleSkillInstall(rec, httpReq)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("duplicate install = %d, want 500", rec.Code)
	}
}

func TestSkillRemove_ErrorPaths(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.skillInstaller = skills.NewSkillInstaller(t.TempDir())

	// Removing a non-existent skill → installer error → 500.
	rec := httptest.NewRecorder()
	httpReq := authenticatedRequest(t, ts, "/api/v1/skills/nope/remove")
	httpReq.SetPathValue("name", "nope")
	ts.channel.handleSkillRemove(rec, httpReq)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("non-existent remove = %d, want 500", rec.Code)
	}

	// Missing name → 400.
	rec = httptest.NewRecorder()
	httpReq = authenticatedRequest(t, ts, "/api/v1/skills//remove")
	httpReq.SetPathValue("name", "")
	ts.channel.handleSkillRemove(rec, httpReq)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing name = %d, want 400", rec.Code)
	}
}

func TestSkillScan_ErrorPaths(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.skillInstaller = skills.NewSkillInstaller(t.TempDir())

	// Invalid body → 400.
	rec := httptest.NewRecorder()
	httpReq := authenticatedRequest(t, ts, "/api/v1/skills/scan")
	httpReq.Method = http.MethodPost
	httpReq.Body = http.NoBody
	ts.channel.handleSkillScan(rec, httpReq)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid body = %d, want 400", rec.Code)
	}

	// Empty repo → 400.
	rec = httptest.NewRecorder()
	httpReq = authenticatedRequest(t, ts, "/api/v1/skills/scan")
	httpReq.Method = http.MethodPost
	httpReq.Body = mkBody(`{"repo":""}`)
	ts.channel.handleSkillScan(rec, httpReq)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty repo = %d, want 400", rec.Code)
	}

	// Valid repo but offline → scan may succeed with zero skills OR return an
	// error; we only assert no panic and a non-5xx code we can rely on. To keep
	// the test deterministic, drop the network-dependent branch.
}

func TestSkillInstallBatch_ErrorPaths(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.skillInstaller = skills.NewSkillInstaller(t.TempDir())

	// Invalid body → 400.
	rec := httptest.NewRecorder()
	httpReq := authenticatedRequest(t, ts, "/api/v1/skills/install-batch")
	httpReq.Method = http.MethodPost
	httpReq.Body = http.NoBody
	ts.channel.handleSkillInstallBatch(rec, httpReq)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid body = %d, want 400", rec.Code)
	}

	// Empty repo → 400.
	rec = httptest.NewRecorder()
	httpReq = authenticatedRequest(t, ts, "/api/v1/skills/install-batch")
	httpReq.Method = http.MethodPost
	httpReq.Body = mkBody(`{"repo":"","skills":["a"]}`)
	ts.channel.handleSkillInstallBatch(rec, httpReq)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty repo = %d, want 400", rec.Code)
	}

	// Empty skills list → 400.
	rec = httptest.NewRecorder()
	httpReq = authenticatedRequest(t, ts, "/api/v1/skills/install-batch")
	httpReq.Method = http.MethodPost
	httpReq.Body = mkBody(`{"repo":"owner/repo","skills":[]}`)
	ts.channel.handleSkillInstallBatch(rec, httpReq)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty skills = %d, want 400", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// rest_updates.go — nil-service and offline-checker branches
// ---------------------------------------------------------------------------

func TestUpdatesConfigPathUnreadableDefaultsEnabled(t *testing.T) {
	n := &NativeChannel{configPath: filepath.Join(t.TempDir(), "missing.json")}
	if !n.updatesEnabled() {
		t.Error("unreadable config should default to enabled")
	}
}

func TestUpdatesCheck_WithServiceOffline(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.SetUpdateService(newOfflineUpdater(t))

	rec := httptest.NewRecorder()
	httpReq := authenticatedRequest(t, ts, "/api/v1/system/updates/check")
	ts.channel.handleUpdatesCheck(rec, httpReq)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("offline check = %d, want 502", rec.Code)
	}
}

func TestUpdatesStatus_WithoutService(t *testing.T) {
	// newNativeTestServer's channel has no update service set → 503.
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	httpReq := authenticatedRequest(t, ts, "/api/v1/system/updates/status")
	ts.channel.handleUpdatesStatus(rec, httpReq)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no service = %d, want 503", rec.Code)
	}
}

func TestUpdatesApply_NoService(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	httpReq := authenticatedRequest(t, ts, "/api/v1/system/updates/apply")
	httpReq.Method = http.MethodPost
	ts.channel.handleUpdatesApply(rec, httpReq)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no service = %d, want 503", rec.Code)
	}
}

func TestUpdatesRollback_WithoutService(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	httpReq := authenticatedRequest(t, ts, "/api/v1/system/updates/rollback")
	httpReq.Method = http.MethodPost
	ts.channel.handleUpdatesRollback(rec, httpReq)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no service = %d, want 503", rec.Code)
	}
}

// newOfflineUpdater returns an Updater whose checker will always fail (no
// network reachability), so Check returns an error.
func newOfflineUpdater(t *testing.T) *update.Updater {
	t.Helper()
	u := update.NewUpdater("", t.TempDir(), "0.1.0")
	u.Checker.Client = &http.Client{
		Timeout: 30 * time.Second,
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("forced offline failure")
		}),
	}
	return u
}

// roundTripperFunc adapts a function into an http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// ---------------------------------------------------------------------------
// native.go — background exec stream / stop / output error branches
// ---------------------------------------------------------------------------

func TestBackgroundExecOutput_MissingID(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	httpReq := authenticatedRequest(t, ts, "/api/v1/background-exec/")
	ts.channel.handleBackgroundExecOutput(rec, httpReq)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing id = %d, want 400", rec.Code)
	}
}

func TestBackgroundExecOutput_NotFound(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	httpReq := authenticatedRequest(t, ts, "/api/v1/background-exec/xyz123?tail=10")
	httpReq.SetPathValue("id", "xyz123")
	ts.channel.handleBackgroundExecOutput(rec, httpReq)
	if rec.Code != http.StatusNotFound {
		t.Errorf("not found = %d, want 404", rec.Code)
	}
}

func TestBackgroundExecStop_MissingID(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	httpReq := authenticatedRequest(t, ts, "/api/v1/background-exec//stop")
	httpReq.Method = http.MethodPost
	ts.channel.handleBackgroundExecStop(rec, httpReq)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing id = %d, want 400", rec.Code)
	}
}

func TestBackgroundExecStop_NotFound(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	httpReq := authenticatedRequest(t, ts, "/api/v1/background-exec/nope/stop")
	httpReq.Method = http.MethodPost
	httpReq.SetPathValue("id", "nope")
	ts.channel.handleBackgroundExecStop(rec, httpReq)
	if rec.Code != http.StatusNotFound {
		t.Errorf("not found = %d, want 404", rec.Code)
	}
}

func TestBackgroundExecStream_MissingID(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	httpReq := authenticatedRequest(t, ts, "/api/v1/background-exec//stream")
	ts.channel.handleBackgroundExecStream(rec, httpReq)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing id = %d, want 400", rec.Code)
	}
}

func TestBackgroundExecStream_NoFlusher(t *testing.T) {
	ts := newNativeTestServer(t)
	n := &NativeChannel{agentLoop: ts.loop}
	rec := &nonFlusherRecorder{}
	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/background-exec/x/stream", nil)
	httpReq.SetPathValue("id", "x")
	n.handleBackgroundExecStream(rec, httpReq)
	if rec.status != http.StatusInternalServerError {
		t.Errorf("no flusher = %d, want 500", rec.status)
	}
}

// nonFlusherRecorder implements http.ResponseWriter but deliberately does NOT
// implement http.Flusher, so the handler's streaming-not-supported branch
// triggers.
type nonFlusherRecorder struct {
	status int
	header http.Header
	body   bytes.Buffer
}

func (r *nonFlusherRecorder) Header() http.Header {
	if r.header == nil {
		r.header = make(http.Header)
	}
	return r.header
}

func (r *nonFlusherRecorder) Write(b []byte) (int, error) { return r.body.Write(b) }

func (r *nonFlusherRecorder) WriteHeader(code int) { r.status = code }

// ---------------------------------------------------------------------------
// discord.go — downloadAttachment against a live httptest server
// ---------------------------------------------------------------------------

func TestDiscordDownloadAttachment(t *testing.T) {
	content := []byte("fake audio bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()

	ch := newTestDiscord(t, nil)
	path := ch.downloadAttachment(srv.URL+"/audio.mp3", "audio.mp3")
	if path == "" {
		t.Fatalf("downloadAttachment returned empty path")
	}
	defer os.Remove(path)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("downloaded content mismatch")
	}
}

func TestDiscordDownloadAttachment_Unreachable(t *testing.T) {
	ch := newTestDiscord(t, nil)
	path := ch.downloadAttachment("http://127.0.0.1:1/nope.mp3", "nope.mp3")
	if path != "" {
		os.Remove(path)
		t.Errorf("expected empty path for unreachable host, got %q", path)
	}
}

// ---------------------------------------------------------------------------
// slack.go — Start with a mock auth.test server
// ---------------------------------------------------------------------------

func TestSlackStart_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "auth.test"):
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "user_id": "U123", "team_id": "T1"})
		default:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		}
	}))
	defer srv.Close()

	cfg := config.SlackConfig{BotToken: "xoxb-test", AppToken: "xapp-test"}
	mb := bus.NewMessageBus()
	ch, err := NewSlackChannel(cfg, mb)
	if err != nil {
		t.Fatalf("NewSlackChannel: %v", err)
	}
	ch.api = slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !ch.IsRunning() {
		t.Error("channel should be running after Start")
	}
}

func TestSlackStart_AuthFailure(t *testing.T) {
	// Mock returns ok:false → AuthTest fails → Start returns error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "invalid_auth"})
	}))
	defer srv.Close()

	cfg := config.SlackConfig{BotToken: "xoxb-test", AppToken: "xapp-test"}
	mb := bus.NewMessageBus()
	ch, err := NewSlackChannel(cfg, mb)
	if err != nil {
		t.Fatalf("NewSlackChannel: %v", err)
	}
	ch.api = slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ch.Start(ctx); err == nil {
		t.Error("expected error from failed auth.test")
	}
	if ch.IsRunning() {
		t.Error("channel should not be running after auth failure")
	}
}

// ---------------------------------------------------------------------------
// onebot.go — Send / buildSendRequest / connect error spans
// ---------------------------------------------------------------------------

func TestOneBot_Send_NotConnectedGroupV7(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{WSUrl: "ws://localhost:1"}, mb)
	ch.setRunning(true)
	if err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "group:abc", Content: "hi"}); err == nil {
		t.Error("expected error when not connected")
	}
}

func TestOneBot_buildSendRequest_InvalidID(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, bus.NewMessageBus())
	_, _, err := ch.buildSendRequest(bus.OutboundMessage{ChatID: "group:not-a-number", Content: "hi"})
	if err == nil {
		t.Error("expected error for non-numeric id")
	}
}

func TestOneBot_connect_Unreachable(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{WSUrl: "ws://127.0.0.1:1"}, bus.NewMessageBus())
	ch.ctx, ch.cancel = context.WithCancel(context.Background())
	defer ch.cancel()
	if err := ch.connect(); err == nil {
		t.Error("expected connect error for unreachable host")
	}
}

// ---------------------------------------------------------------------------
// line.go — processEvent branches
// ---------------------------------------------------------------------------

func TestLINE_ProcessEvent_NonMessage(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewLINEChannel(config.LINEConfig{ChannelAccessToken: "t", ChannelSecret: "s"}, mb)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	buf := consumeInbound(t, mb, ctx)

	ch.processEvent(lineEvent{Type: "join"})
	if len(buf) != 0 {
		t.Error("non-message event should be ignored")
	}
}
