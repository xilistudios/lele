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

type noopCloser struct{}

func (noopCloser) Close() {}

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

	select {
	case <-aborted:
	case <-time.After(5 * time.Second):
		t.Error("the provider stream was not aborted: cancelling the root context must kill the turn's request")
	}
}
