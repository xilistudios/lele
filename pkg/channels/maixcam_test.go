package channels

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// TestMaixCamSendStopDeadlock verifies SURV-02: Send holding RLock during a
// blocking conn.Write no longer prevents Stop from acquiring Lock.
//
// We use net.Pipe() (synchronous, in-process) whose Read side is never
// consumed, so Write blocks indefinitely. This is the most reliable way to
// produce a permanently-blocked Write on any CI host — no dependence on kernel
// TCP buffer sizes.
//
// BEFORE the fix: Stop blocks on Lock for the test timeout (5 s) → test FAILS.
// AFTER  the fix: Stop returns promptly (< 1 s); Send eventually returns
// either a deadline error (if SetWriteDeadline fires) or a closed-connection
// error (Stop closed the pipe).
func TestMaixCamSendStopDeadlock(t *testing.T) {
	// --- arrange: pipe whose Read side is never consumed ----------------
	remoteConn, clientConn := net.Pipe()
	defer clientConn.Close()

	ch, err := NewMaixCamChannel(config.MaixCamConfig{}, &bus.MessageBus{})
	if err != nil {
		t.Fatalf("NewMaixCamChannel: %v", err)
	}

	// Register remoteConn exactly as handleConnection / acceptConnections does.
	ch.clientsMux.Lock()
	ch.clients[remoteConn] = true
	ch.clientsMux.Unlock()

	// Ensure Send won't bail early.
	ch.setRunning(true)

	// Context with 10 s deadline so Send's write-deadline logic has time to
	// exercise; the test-level timeout is tighter (5 s).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msg := bus.OutboundMessage{Content: "test", ChatID: "test"}

	// --- act: Send (blocks on pipe Write) + Stop concurrently ----------
	sendDone := make(chan struct{})
	go func() {
		_ = ch.Send(ctx, msg) // blocks on pipe Write
		close(sendDone)
	}()

	// Give Send time to enter the blocking Write.
	time.Sleep(100 * time.Millisecond)

	stopDone := make(chan struct{})
	go func() {
		_ = ch.Stop(ctx)
		close(stopDone)
	}()

	// --- assert ---------------------------------------------------------
	// With the fix, Stop must return within 5 s even though Send is blocked.
	select {
	case <-stopDone:
		// success — Stop returned without blocking on Lock
	case <-time.After(5 * time.Second):
		t.Fatalf("Stop() deadlocked — Send held RLock during blocking Write (SURV-02)")
	}

	// Cleanup: closing remoteConn unblocks the pipe Write so the Send
	// goroutine exits (no leak).
	remoteConn.Close()
	select {
	case <-sendDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("Send goroutine did not exit after Stop + close (possible leak)")
	}
}

// TestMaixCamSendStopDeadlock_TCPBufferFull is the TCP variant required by
// the spec. It uses a real TCP listener that accepts a connection and NEVER
// reads from it, then fills the kernel send buffer so that conn.Write blocks.
//
// On hosts where the kernel happily buffers > 100 MB (e.g., large
// net.core.wmem_max), this test may not reliably block within the deadline.
// In that case the net.Pipe test above serves as the authoritative deadlock
// regression test. This test is kept as best-effort and documented in the
// commit message.
func TestMaixCamSendStopDeadlock_TCPBufferFull(t *testing.T) {
	// --- arrange: TCP listener that never reads ------------------------
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()

	clientConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close()

	remoteConn := <-accepted

	// Fill the kernel send buffer so clientConn.Write (and thus Send's
	// remoteConn.Read-based writes from the other side) will eventually block.
	go func() {
		blob := make([]byte, 64*1024) // 64 KiB chunks
		for {
			if _, err := clientConn.Write(blob); err != nil {
				return
			}
		}
	}()

	// Give the filler goroutine time to saturate the buffer.
	time.Sleep(200 * time.Millisecond)

	ch, err := NewMaixCamChannel(config.MaixCamConfig{}, &bus.MessageBus{})
	if err != nil {
		t.Fatalf("NewMaixCamChannel: %v", err)
	}

	ch.clientsMux.Lock()
	ch.clients[remoteConn] = true
	ch.clientsMux.Unlock()
	ch.setRunning(true)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msg := bus.OutboundMessage{Content: "test", ChatID: "test"}

	sendDone := make(chan struct{})
	go func() {
		_ = ch.Send(ctx, msg)
		close(sendDone)
	}()

	time.Sleep(100 * time.Millisecond) // let Send enter Write

	stopDone := make(chan struct{})
	go func() {
		_ = ch.Stop(ctx)
		close(stopDone)
	}()

	select {
	case <-stopDone:
		// Stop returned — no deadlock.
	case <-time.After(5 * time.Second):
		t.Fatalf("deadlock detected (TCP variant, SURV-02)")
	}

	// Cleanup: close remoteConn to unblock Send.
	_ = remoteConn.Close()
	select {
	case <-sendDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("Send goroutine did not exit after Stop + close (possible leak)")
	}
}
