package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// noopCloser stands in for *tui.Model when a test only cares about the agent
// side of the exit path.
type noopCloser struct{}

func (noopCloser) Close() {}

// slowProvider starts an OpenAI-compatible streaming endpoint that accepts the
// request and then never sends a result token, so the turn stays in flight
// until something cancels it (exactly the state that used to hold the shell).
//
// `started` receives one value per accepted request. `aborted` receives one
// value once the client's connection is really gone — a plain read of r.Context
// is not enough here, because a handler that never writes is not told about a
// client that went away; the periodic keep-alive comment is what makes the
// disconnect observable.
func slowProvider(t *testing.T) (baseURL string, started, aborted <-chan struct{}) {
	t.Helper()

	startedCh := make(chan struct{}, 4)
	abortedCh := make(chan struct{}, 4)
	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case startedCh <- struct{}{}:
		default:
		}
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		if flusher != nil {
			flusher.Flush()
		}

		markAborted := func() {
			select {
			case abortedCh <- struct{}{}:
			default:
			}
		}

		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-release:
				return
			case <-r.Context().Done():
				markAborted()
				return
			case <-ticker.C:
				// An SSE comment: ignored by the parser, but the write fails
				// as soon as the client is gone.
				if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
					markAborted()
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}))

	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	return srv.URL + "/v1", startedCh, abortedCh
}

// TestShutdownTUI_ReleasesRealInFlightTurnWithinGrace is the observable form of
// the exit fix, on a real *agent.AgentLoop: a turn is genuinely in flight (the
// provider stream is open and has produced no answer, which is what used to
// hold the shell for the rest of the response), and the teardown must finish far
// inside the grace because cancelling the root context aborts that stream.
//
// It deliberately drives the production helper instead of asserting on fake
// call records: the guarantee under test is "the application terminates within
// the grace while a turn runs", not "this function calls that function".
func TestShutdownTUI_ReleasesRealInFlightTurnWithinGrace(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	baseURL, started, aborted := slowProvider(t)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "fake:slow",
				MaxTokens:         1024,
				MaxToolIterations: 2,
			},
		},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"fake": {
					Type: "openai",
					ProviderConfig: config.ProviderConfig{
						APIKey:  "sk-fake",
						APIBase: baseURL,
					},
					Models: map[string]config.ProviderModelConfig{"slow": {Model: "slow-model"}},
				},
			},
		},
	}

	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus())
	if loop == nil {
		t.Fatal("NewAgentLoop returned nil")
	}
	rootCtx, cancel := context.WithCancel(context.Background())
	go func() { _ = loop.Run(rootCtx) }()

	// Same inbound shape the TUI publishes for a user message.
	const sessionKey = "tui:chat:shutdown-test"
	loop.MessageBus().PublishInbound(bus.InboundMessage{
		Channel:    "native",
		SenderID:   "tui",
		ChatID:     sessionKey,
		Content:    "hello",
		SessionKey: sessionKey,
	})

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the agent loop never reached the provider; no turn was in flight")
	}

	start := time.Now()
	shutdownTUI(noopCloser{}, cancel, loop)
	elapsed := time.Since(start)

	if elapsed > tuiExitJoinGrace/4 {
		t.Errorf("teardown took %s with a %s grace: the in-flight request must be aborted, not awaited",
			elapsed, tuiExitJoinGrace)
	}
	t.Logf("teardown of a live turn returned in %s (grace %s)", elapsed, tuiExitJoinGrace)

	// The provider stream must be gone by the time we return: a process that is
	// exiting may not leave the turn's request running.
	select {
	case <-aborted:
	case <-time.After(5 * time.Second):
		t.Error("the provider stream was not aborted: cancelling the root context must kill the turn's request")
	}
}
