package devices

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/devices/events"
	"github.com/xilistudios/lele/pkg/state"
)

// mockSource is a controllable EventSource for tests.
type mockSource struct {
	kind     events.Kind
	startFn  func(ctx context.Context) (<-chan *events.DeviceEvent, error)
	stopErr  error
	stopped  bool
	startErr error
}

func (m *mockSource) Kind() events.Kind { return m.kind }
func (m *mockSource) Start(ctx context.Context) (<-chan *events.DeviceEvent, error) {
	if m.startErr != nil {
		return nil, m.startErr
	}
	if m.startFn != nil {
		return m.startFn(ctx)
	}
	ch := make(chan *events.DeviceEvent)
	return ch, nil
}
func (m *mockSource) Stop() error {
	m.stopped = true
	return m.stopErr
}

func newTestStateManager(t *testing.T) *state.Manager {
	t.Helper()
	dir := t.TempDir()
	sm := state.NewManager(dir)
	if sm == nil {
		// Fall back to a direct workspace path (NewManager may return nil on failure)
		ws := filepath.Join(dir, "ws")
		_ = os.MkdirAll(ws, 0755)
		sm = state.NewManager(ws)
	}
	if sm == nil {
		t.Fatal("failed to create state manager")
	}
	return sm
}

func TestNewService(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		expected int // number of sources on linux when enabled+monitorUSB
	}{
		{name: "disabled", cfg: Config{Enabled: false}, expected: 0},
		{name: "enabled no monitor", cfg: Config{Enabled: true, MonitorUSB: false}, expected: 0},
		{name: "enabled monitor usb", cfg: Config{Enabled: true, MonitorUSB: true}, expected: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewService(tt.cfg, newTestStateManager(t))
			if s == nil {
				t.Fatal("NewService returned nil")
			}
			if s.enabled != tt.cfg.Enabled {
				t.Errorf("enabled = %v, want %v", s.enabled, tt.cfg.Enabled)
			}
			if s.bus != nil {
				t.Errorf("bus should be nil initially")
			}
			// On linux, MonitorUSB adds the udevadm source.
			if tt.cfg.Enabled && tt.cfg.MonitorUSB && len(s.sources) != 1 {
				t.Errorf("sources len = %d, want 1", len(s.sources))
			}
			if !tt.cfg.MonitorUSB && len(s.sources) != 0 {
				t.Errorf("sources len = %d, want 0", len(s.sources))
			}
		})
	}
}

func TestService_SetBus(t *testing.T) {
	s := NewService(Config{}, newTestStateManager(t))
	mb := bus.NewMessageBus()
	s.SetBus(mb)
	s.mu.RLock()
	got := s.bus
	s.mu.RUnlock()
	if got != mb {
		t.Errorf("bus not set correctly")
	}
}

func TestService_StartDisabled(t *testing.T) {
	s := NewService(Config{Enabled: false}, newTestStateManager(t))
	if err := s.Start(context.Background()); err != nil {
		t.Errorf("Start on disabled service returned error: %v", err)
	}
}

func TestService_StartNoSources(t *testing.T) {
	s := NewService(Config{Enabled: true, MonitorUSB: false}, newTestStateManager(t))
	if err := s.Start(context.Background()); err != nil {
		t.Errorf("Start with no sources returned error: %v", err)
	}
}

func TestService_StartAndStopWithMockSource(t *testing.T) {
	s := NewService(Config{Enabled: true}, newTestStateManager(t))
	ch := make(chan *events.DeviceEvent)
	close(ch)
	src := &mockSource{kind: events.KindUSB,
		startFn: func(ctx context.Context) (<-chan *events.DeviceEvent, error) { return ch, nil },
	}
	s.sources = []events.EventSource{src}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if s.cancel == nil {
		t.Errorf("cancel func should be set after Start")
	}
	s.Stop()
	if s.cancel != nil {
		t.Errorf("cancel should be nil after Stop")
	}
}

func TestService_StartSourceError(t *testing.T) {
	s := NewService(Config{Enabled: true}, newTestStateManager(t))
	src := &mockSource{kind: events.KindUSB, startErr: context.Canceled}
	s.sources = []events.EventSource{src}
	// Start should not return the error; it logs and continues.
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start should swallow source errors, got: %v", err)
	}
}

func TestService_StopNoCancel(t *testing.T) {
	s := NewService(Config{Enabled: true}, newTestStateManager(t))
	src := &mockSource{kind: events.KindUSB}
	s.sources = []events.EventSource{src}
	s.Stop() // cancel == nil path
	if !src.stopped {
		t.Errorf("source Stop should be called")
	}
}

func TestService_UpdateConfig(t *testing.T) {
	sm := newTestStateManager(t)
	s := NewService(Config{Enabled: true, MonitorUSB: false}, sm)
	// Enable to create a cancel then disable.
	s.ctx, s.cancel = context.WithCancel(context.Background())

	s.UpdateConfig(Config{Enabled: true, MonitorUSB: false})
	if !s.enabled {
		t.Errorf("enabled should remain true")
	}
	if s.cancel == nil {
		t.Errorf("cancel should remain set when still enabled")
	}

	// Now disable -> cancel should be cleared.
	s.UpdateConfig(Config{Enabled: false})
	if s.enabled {
		t.Errorf("enabled should be false")
	}
	if s.cancel != nil {
		t.Errorf("cancel should be nil when disabled")
	}
	if len(s.sources) != 0 {
		t.Errorf("sources should be empty when disabled")
	}
}

func TestParseLastChannel(t *testing.T) {
	tests := []struct {
		in       string
		wantP    string
		wantU    string
	}{
		{"telegram:123", "telegram", "123"},
		{"", "", ""},
		{"nocolon", "", ""},
		{":nouser", "", ""},
		{"telegram:", "", ""},
		{"telegram:abc:extra", "telegram", "abc:extra"},
		{"whatsapp:+15551234", "whatsapp", "+15551234"},
	}
	for _, tt := range tests {
		p, u := parseLastChannel(tt.in)
		if p != tt.wantP || u != tt.wantU {
			t.Errorf("parseLastChannel(%q) = (%q,%q), want (%q,%q)", tt.in, p, u, tt.wantP, tt.wantU)
		}
	}
}

func TestService_handleEvents(t *testing.T) {
	s := NewService(Config{Enabled: true}, newTestStateManager(t))
	ch := make(chan *events.DeviceEvent)

	var published []bus.OutboundMessage
	mb := bus.NewMessageBus()
	s.SetBus(mb)

	// Set last channel so notifications are attempted.
	_ = s.state.SetLastChannel("telegram:user456")

	// Publish loop.
	go func() {
		ch <- nil
		ch <- &events.DeviceEvent{Action: events.ActionAdd, Kind: events.KindUSB, Vendor: "V", Product: "P"}
		close(ch)
	}()

	// Reader to capture outbound messages.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			msg, ok := mb.SubscribeOutbound(ctx)
			if !ok {
				return
			}
			published = append(published, msg)
		}
	}()

	s.handleEvents(events.KindUSB, ch)
	time.Sleep(50 * time.Millisecond) // allow reader goroutine to drain
	cancel()

	if len(published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(published))
	}
	pm := published[0]
	if pm.Channel != "telegram" || pm.ChatID != "user456" {
		t.Errorf("unexpected outbound message: %+v", pm)
	}
	if !containsStr(pm.Content, "Connected") {
		t.Errorf("message content missing marker: %q", pm.Content)
	}
}

func TestService_sendNotification_NilBus(t *testing.T) {
	s := NewService(Config{Enabled: true}, newTestStateManager(t))
	// bus is nil -> no panic
	s.sendNotification(&events.DeviceEvent{Action: events.ActionAdd})
}

func TestService_sendNotification_NoLastChannel(t *testing.T) {
	s := NewService(Config{Enabled: true}, newTestStateManager(t))
	mb := bus.NewMessageBus()
	s.SetBus(mb)
	// last channel empty -> skips.
	s.sendNotification(&events.DeviceEvent{Action: events.ActionAdd, Kind: events.KindUSB})
}

func TestService_sendNotification_InternalChannel(t *testing.T) {
	s := NewService(Config{Enabled: true}, newTestStateManager(t))
	_ = s.state.SetLastChannel("cli:me")
	mb := bus.NewMessageBus()
	s.SetBus(mb)
	s.sendNotification(&events.DeviceEvent{Action: events.ActionAdd, Kind: events.KindUSB})
}

func TestService_sendNotification_BadLastChannel(t *testing.T) {
	s := NewService(Config{Enabled: true}, newTestStateManager(t))
	_ = s.state.SetLastChannel("notvalid")
	mb := bus.NewMessageBus()
	s.SetBus(mb)
	s.sendNotification(&events.DeviceEvent{Action: events.ActionAdd, Kind: events.KindUSB})
}

func containsStr(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}