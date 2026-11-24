package openai_compat

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/xilistudios/lele/pkg/providers/common"
)

func TestParseSSEStreamMidStreamError(t *testing.T) {
	sseData := "data: {\"choices\":[{\"delta\":{\"content\":\"hello wor\"}}]}\n\n"

	readErr := errors.New("connection reset by peer")
	body := io.MultiReader(strings.NewReader(sseData), iotest.ErrReader(readErr))

	doneCalled := false
	resp, err := parseSSEStream(context.Background(), body, func(chunk string, done bool) {
		if done {
			doneCalled = true
		}
	}, nil)

	if err == nil {
		t.Fatal("expected an error for a truncated stream, got nil")
	}
	if !errors.Is(err, common.ErrStreamTruncated) {
		t.Fatalf("error = %v, want it to wrap common.ErrStreamTruncated", err)
	}
	if !errors.Is(err, readErr) {
		t.Fatalf("error = %v, want it to wrap the reader error", err)
	}
	if doneCalled {
		t.Fatal("onChunk(\"\", true) must not be called on a truncated stream")
	}
	if resp == nil {
		t.Fatal("expected partial response alongside the error")
	}
	if resp.Content != "hello wor" {
		t.Fatalf("partial content = %q, want %q", resp.Content, "hello wor")
	}
}

func TestParseSSEStreamTooLongLine(t *testing.T) {
	line := "data: {\"pad\":\"" + strings.Repeat("x", 2*1024*1024) + "\"}\n"

	doneCalled := false
	_, err := parseSSEStream(context.Background(), strings.NewReader(line), func(chunk string, done bool) {
		if done {
			doneCalled = true
		}
	}, nil)

	if err == nil {
		t.Fatal("expected an error for an over-long SSE line, got nil")
	}
	if !errors.Is(err, common.ErrStreamTruncated) {
		t.Fatalf("error = %v, want it to wrap common.ErrStreamTruncated", err)
	}
	if !strings.Contains(err.Error(), "1 MB") {
		t.Fatalf("error = %q, want it to name the 1 MB line limit", err.Error())
	}
	if doneCalled {
		t.Fatal("onChunk(\"\", true) must not be called for an over-long line")
	}
}

func TestParseSSEStreamCleanEOFIsSuccess(t *testing.T) {
	sseData := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"

	doneCalled := false
	resp, err := parseSSEStream(context.Background(), strings.NewReader(sseData), func(chunk string, done bool) {
		if done {
			doneCalled = true
		}
	}, nil)
	if err != nil {
		t.Fatalf("parseSSEStream() error = %v, want nil", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("Content = %q, want %q", resp.Content, "ok")
	}
	if !doneCalled {
		t.Fatal("onChunk(\"\", true) must be called on a clean stream")
	}
}
