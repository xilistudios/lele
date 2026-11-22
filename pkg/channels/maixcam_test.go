package channels

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func TestMaixCamSendStopDeadlock(t *testing.T) {
	remoteConn, clientConn := net.Pipe()
	defer clientConn.Close()

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

	time.Sleep(100 * time.Millisecond)

	stopDone := make(chan struct{})
	go func() {
		_ = ch.Stop(ctx)
		close(stopDone)
	}()

	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("Stop() deadlocked — Send held RLock during blocking Write (SURV-02)")
	}

	remoteConn.Close()
	select {
	case <-sendDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("Send goroutine did not exit after Stop + close (possible leak)")
	}
}

func TestMaixCamSendStopDeadlock_TCPBufferFull(t *testing.T) {
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

	go func() {
		blob := make([]byte, 64*1024)
		for {
			if _, err := clientConn.Write(blob); err != nil {
				return
			}
		}
	}()

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

	time.Sleep(100 * time.Millisecond)

	stopDone := make(chan struct{})
	go func() {
		_ = ch.Stop(ctx)
		close(stopDone)
	}()

	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("deadlock detected (TCP variant, SURV-02)")
	}

	_ = remoteConn.Close()
	select {
	case <-sendDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("Send goroutine did not exit after Stop + close (possible leak)")
	}
}
