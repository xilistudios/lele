//go:build linux || darwin

package keepawake

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const stderrCap = 4096

func startCommand(cmd *exec.Cmd) (func(), error) {
	buf := &limitedWriter{}
	cmd.Stderr = buf
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("keep-awake: starting %s: %w", cmd.Path, err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil, fmt.Errorf("keep-awake: %s exited immediately: %s",
			filepath.Base(cmd.Path), firstLineOrNoOutput(buf.String()))
	case <-time.After(250 * time.Millisecond):
	}
	pid := cmd.Process.Pid
	var once sync.Once
	return func() {
		once.Do(func() {
			select {
			case <-done:
				return
			default:
			}
			_ = syscall.Kill(-pid, syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			select {
			case <-done:
			case <-time.After(1 * time.Second):
			}
		})
	}, nil
}

type limitedWriter struct {
	mu  sync.Mutex
	buf []byte
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if room := stderrCap - len(w.buf); room > 0 {
		if len(p) > room {
			w.buf = append(w.buf, p[:room]...)
		} else {
			w.buf = append(w.buf, p...)
		}
	}
	return len(p), nil
}

func (w *limitedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}

func firstLineOrNoOutput(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return truncateReason(trimmed)
		}
	}
	return "no output"
}

const maxReasonLen = 512

func truncateReason(s string) string {
	if len(s) <= maxReasonLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxReasonLen {
		return s
	}
	return string(runes[:maxReasonLen]) + "…"
}
