package main

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestWebServeCmd_Subprocess starts webServeCmd in a child process via the
// LELE_TEST_WEB TestMain route, waits for the web app to bind and serve, then
// sends SIGINT to trigger graceful shutdown. Covers fs.Sub, handler
// construction, server bind, signal handling and the clean shutdown path of
// webServeCmd. Child coverage is merged via the standard re-exec mechanism.
func TestWebServeCmd_Subprocess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestMainPlaceholder")
	cmd.Env = append(os.Environ(), "LELE_TEST_WEB=1")
	done := make(chan struct{})
	var output []byte
	go func() {
		output, _ = cmd.CombinedOutput()
		close(done)
	}()

	time.Sleep(1 * time.Second)
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		cmd.Process.Kill()
		t.Fatalf("send SIGINT: %v", err)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("web subprocess did not exit in time")
	}
	<-done

	out := string(output)
	if !strings.Contains(out, "Serving web app on") {
		t.Errorf("expected serving message, output:\n%s", out)
	}
}