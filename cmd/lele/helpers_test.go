package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

// stdinPipe represents a redirected stdin that can be fed input.
type stdinPipe struct {
	orig  *os.File
	r     *os.File
	w     *os.File
	t     *testing.T
	mutex sync.Mutex
	closed bool
}

// newStdinPipe creates a pipe and redirects os.Stdin to its read end.
func newStdinPipe(t *testing.T) *stdinPipe {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		r.Close()
		w.Close()
	})
	return &stdinPipe{orig: orig, r: r, w: w, t: t}
}

// feed writes a line to the stdin pipe.
func (p *stdinPipe) feed(line string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.closed {
		return
	}
	if _, err := io.WriteString(p.w, line); err != nil {
		p.t.Errorf("write to stdin pipe: %v", err)
	}
}

// feedLines writes multiple lines.
func (p *stdinPipe) feedLines(lines ...string) {
	for _, l := range lines {
		p.feed(l + "\n")
	}
}

// close closes the write end so blocking reads (e.g. EOF) terminate.
func (p *stdinPipe) close() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if !p.closed {
		p.w.Close()
		p.closed = true
	}
	p.closed = true
}

// captureStdout redirects stdout and returns a function that collects output.
// The returned collect() returns all output written since the redirect began.
func captureStdout(t *testing.T) func() string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	t.Cleanup(func() {
		os.Stdout = old
		w.Close()
		r.Close()
	})

	var mu sync.Mutex
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(&buf, r)
	}()

	// Return a collect func; closing the pipe terminates the goroutine.
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		w.Close()
		<-done
		return buf.String()
	}
}

// replaceArgs temporarily replaces os.Args for the duration of f.
func replaceArgs(t *testing.T, args []string) {
	t.Helper()
	orig := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = orig })
}

// runCmd runs f, capturing stdout into a string.
func runCmd(f func()) string {
	var buf bytes.Buffer
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old
	io.Copy(&buf, r)
	r.Close()
	return buf.String()
}

// execInDir runs a func with a working directory override via env (not used here).
func hasSubstring(t *testing.T, haystack, needle, msg string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%s: output %q does not contain %q", msg, haystack, needle)
	}
}