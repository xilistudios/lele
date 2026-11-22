package channels

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type lifecycleFixture struct {
	channel *NativeChannel
	srv     *httptest.Server
	wsURL   string
	idSeq   int
}

func newLifecycleFixture(t *testing.T) *lifecycleFixture {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	ch := &NativeChannel{
		wsClients: make(map[string]*WSClient),
	}

	ch.base = NewBaseChannel("native", nil, nil, nil)
	ch.pinLimiter = newRateLimiter(10, time.Minute)
	ch.pairLimiter = newRateLimiter(5, time.Minute)
	ch.refreshLimiter = newRateLimiter(20, time.Minute)
	ch.authLogLimiter = newRateLimiter(6, time.Minute)
	ch.apiLimiter = newRateLimiter(120, time.Minute)
	ch.wsMessageLimiter = newRateLimiter(120, time.Minute)

	return &lifecycleFixture{channel: ch, srv: srv, wsURL: wsURL}
}

func (f *lifecycleFixture) newClient(t *testing.T, sessionKey string) *WSClient {
	t.Helper()

	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(f.wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	f.idSeq++
	client := newWSClient(
		fmt.Sprintf("lifecycle-%s-%d", sessionKey, f.idSeq),
		conn,
		&ClientInfo{ClientID: "test-client"},
		sessionKey,
	)
	f.channel.addWSClient(client)
	t.Cleanup(func() { f.channel.removeWSClient(client.ID) })

	return client
}

func newBareClient(sessionKey string) *WSClient {
	return &WSClient{
		ID:            fmt.Sprintf("bare-%s", sessionKey),
		SessionKey:    sessionKey,
		Subscriptions: map[string]bool{sessionKey: true},
		SendChan:      make(chan []byte, 100),
		done:          make(chan struct{}),
		ClientInfo:    &ClientInfo{ClientID: "test-client"},
	}
}

func TestGW_H4_ConcurrentSubscriptionAccess(t *testing.T) {
	client := newBareClient("race-sess")
	const goroutines = 4
	const iterations = 5000

	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				key := fmt.Sprintf("sess-%d-%d", idx, j%10)
				client.mu.Lock()
				client.SessionKey = key
				if client.Subscriptions == nil {
					client.Subscriptions = make(map[string]bool)
				}
				client.Subscriptions[key] = true
				client.mu.Unlock()
			}
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				client.mu.Lock()
				subs := make([]string, 0, len(client.Subscriptions))
				for k := range client.Subscriptions {
					subs = append(subs, k)
				}
				client.mu.Unlock()
				for _, s := range subs {
					_ = sessionKeyMatches(s, "sess-1")
				}
			}
		}()
	}

	wg.Wait()
}

func TestGW_H4_BroadcastConcurrentWithSubscribe(t *testing.T) {
	fix := newLifecycleFixture(t)
	client := fix.newClient(t, "race-sess")

	var wg sync.WaitGroup
	const iterations = 2000

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			key := fmt.Sprintf("sub-%d", i%5)
			client.mu.Lock()
			if client.Subscriptions == nil {
				client.Subscriptions = make(map[string]bool)
			}
			client.Subscriptions[key] = true
			client.mu.Unlock()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			fix.channel.broadcastToSession("race-sess", "test.event",
				map[string]string{"i": fmt.Sprintf("%d", i)})
		}
	}()

	wg.Wait()
}

func TestSURV_01_WriteLoopCounterAfterReconnect(t *testing.T) {
	fix := newLifecycleFixture(t)
	client := fix.newClient(t, "reconn-sess")

	go fix.channel.wsWriteLoop(client)

	assertActiveLoopsEventually(t, client, 1, 2*time.Second, "after initial connect")

	client.mu.Lock()
	close(client.done)
	client.done = make(chan struct{})
	oldConn := client.Conn
	client.Conn = nil
	client.mu.Unlock()
	if oldConn != nil {
		oldConn.Close()
	}

	assertActiveLoopsEventually(t, client, 0, 2*time.Second, "old loop exited")

	dialer := websocket.Dialer{}
	newConn, _, err := dialer.Dial(fix.wsURL, nil)
	if err != nil {
		t.Fatalf("dial new: %v", err)
	}
	defer newConn.Close()

	client.mu.Lock()
	client.Conn = newConn
	client.mu.Unlock()

	go fix.channel.wsWriteLoop(client)

	assertActiveLoopsEventually(t, client, 1, 2*time.Second, "after reconnect")
}

func TestSURV_01_WriteLoopCounterZeroAfterStop(t *testing.T) {
	fix := newLifecycleFixture(t)
	client := fix.newClient(t, "stop-sess")

	go fix.channel.wsWriteLoop(client)
	assertActiveLoopsEventually(t, client, 1, time.Second, "loop running")

	client.mu.Lock()
	close(client.done)
	client.mu.Unlock()

	assertActiveLoopsEventually(t, client, 0, 2*time.Second, "loop exited")
}

func TestSURV_01_MessageOrderingPreservedAcrossReconnect(t *testing.T) {
	srvReceived := make(chan string, 100)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			srvReceived <- string(msg)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	dialer := websocket.Dialer{}

	conn1, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial1: %v", err)
	}

	ch := &NativeChannel{wsClients: make(map[string]*WSClient)}
	client := &WSClient{
		ID:            "order-client",
		Conn:          conn1,
		SessionKey:    "order-sess",
		Subscriptions: map[string]bool{"order-sess": true},
		SendChan:      make(chan []byte, 100),
		done:          make(chan struct{}),
		ClientInfo:    &ClientInfo{ClientID: "test-client"},
	}
	ch.addWSClient(client)

	go ch.wsWriteLoop(client)

	client.SendChan <- []byte("msg-1")
	client.SendChan <- []byte("msg-2")

	expectMsg(t, srvReceived, "msg-1", 2*time.Second)
	expectMsg(t, srvReceived, "msg-2", 2*time.Second)

	client.mu.Lock()
	close(client.done)
	client.done = make(chan struct{})
	client.Conn = nil
	client.mu.Unlock()
	conn1.Close()

	assertActiveLoopsEventually(t, client, 0, 2*time.Second, "old loop exited")

	conn2, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial2: %v", err)
	}
	defer conn2.Close()

	client.mu.Lock()
	client.Conn = conn2
	client.mu.Unlock()

	go ch.wsWriteLoop(client)

	client.SendChan <- []byte("msg-3")
	client.SendChan <- []byte("msg-4")

	expectMsg(t, srvReceived, "msg-3", 2*time.Second)
	expectMsg(t, srvReceived, "msg-4", 2*time.Second)

	select {
	case extra := <-srvReceived:
		t.Errorf("unexpected extra message: %q", extra)
	case <-time.After(100 * time.Millisecond):
	}

	ch.removeWSClient(client.ID)
}

func TestNoDeadlock_BroadcastDuringReconnection(t *testing.T) {
	fix := newLifecycleFixture(t)
	client := fix.newClient(t, "deadlock-sess")

	go fix.channel.wsWriteLoop(client)

	const cycles = 5

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			fix.channel.broadcastToSession("deadlock-sess", "test.event",
				map[string]string{"seq": fmt.Sprintf("%d", i)})
			time.Sleep(2 * time.Millisecond)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < cycles; i++ {
			time.Sleep(20 * time.Millisecond)

			fix.channel.markWSClientReconnecting(client)
			time.Sleep(20 * time.Millisecond)

			dialer := websocket.Dialer{}
			newConn, _, err := dialer.Dial(fix.wsURL, nil)
			if err != nil {
				return
			}
			fix.channel.reconnectWSClient(client, newConn)
			go fix.channel.wsWriteLoop(client)
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock detected: broadcast + reconnection did not complete within 10s")
	}

	fix.channel.removeWSClient(client.ID)
}

func assertActiveLoopsEventually(t *testing.T, client *WSClient, want int64, timeout time.Duration, label string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if client.activeWriteLoops.Load() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := client.activeWriteLoops.Load()
	if got != want {
		t.Errorf("activeWriteLoops %s = %d after %v, want %d", label, got, timeout, want)
	}
}

func expectMsg(t *testing.T, ch <-chan string, want string, timeout time.Duration) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Errorf("got message %q, want %q", got, want)
		}
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for message %q", want)
	}
}
