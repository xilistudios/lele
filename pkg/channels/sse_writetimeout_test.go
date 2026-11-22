package channels

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

type streamingTestLoop struct {
	*nativeTestAgentLoop
	start time.Time
}

func (l *streamingTestLoop) GetBackgroundExecOutput(id string, tail int) (string, string, int64, error) {
	el := time.Since(l.start)
	out := strings.Repeat("x", int(el.Seconds())+1)
	if el > 3*time.Second {
		return out, "completed", el.Milliseconds(), nil
	}
	return out, "running", el.Milliseconds(), nil
}

func TestSSEBackgroundStreamSurvivesWriteTimeout(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.LeleDir = t.TempDir()

	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	loop := &streamingTestLoop{nativeTestAgentLoop: newNativeTestAgentLoop(cfg), start: time.Now()}
	native, err := NewNativeChannel(cfg, messageBus, loop, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewNativeChannel: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/background-exec/{id}/stream", native.handleBackgroundExecStream)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: mux, WriteTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second}
	go srv.Serve(ln)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer reqCancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://"+ln.Addr().String()+"/api/v1/background-exec/probe-1/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	var sawDone bool
	scanner := bufio.NewScanner(resp.Body)
	deadline := time.After(8 * time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") && strings.Contains(line, `"done":true`) {
				sawDone = true
				return
			}
		}
	}()
	select {
	case <-done:
	case <-deadline:
	}
	if !sawDone {
		t.Fatal("SSE stream was killed before the terminal done event (WriteTimeout not cleared — GW-L9)")
	}
	if el := time.Since(loop.start); el < 2*time.Second {
		t.Fatalf("stream ended suspiciously early (%v): expected >2s of streaming", el)
	}
}
