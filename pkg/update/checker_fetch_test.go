package update

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// roundTripFunc adapts a function to http.RoundTripper so Checker
// requests can be intercepted without real network access.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newInterceptClient(fn roundTripFunc) *http.Client {
	return &http.Client{Transport: roundTripFunc(fn)}
}

// cannedResponse builds an http.Response for the interceptor transport.
func cannedResponse(req *http.Request, code int, body string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode:    code,
		Status:        http.StatusText(code),
		Header:        h,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

func releaseJSON() string {
	b, _ := json.Marshal(Release{Tag: "v9.0.0", Body: "changelog", Assets: []Asset{{Name: "x"}}})
	return string(b)
}

func TestCheckerLatestSuccess(t *testing.T) {
	var gotPath string
	c := &Checker{
		Repo: "owner/repo",
		Client: newInterceptClient(func(req *http.Request) (*http.Response, error) {
			gotPath = req.URL.Path
			if got := req.Header.Get("Accept"); got != "application/vnd.github+json" {
				t.Errorf("Accept header = %q", got)
			}
			if got := req.Header.Get("User-Agent"); got != DefaultUserAgent {
				t.Errorf("User-Agent = %q", got)
			}
			return cannedResponse(req, http.StatusOK, releaseJSON()), nil
		}),
	}

	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Tag != "v9.0.0" {
		t.Errorf("Tag = %q", rel.Tag)
	}
	if !strings.HasSuffix(gotPath, "/releases/latest") {
		t.Errorf("path = %q", gotPath)
	}
}

func TestCheckerLatestSetsAuthToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "sekret")
	var gotAuth string
	c := &Checker{
		Repo: "o/r",
		Client: newInterceptClient(func(req *http.Request) (*http.Response, error) {
			gotAuth = req.Header.Get("Authorization")
			return cannedResponse(req, http.StatusOK, releaseJSON()), nil
		}),
	}
	if _, err := c.Latest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer sekret" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

func TestCheckerByTag(t *testing.T) {
	var gotPath string
	c := &Checker{
		Repo: "o/r",
		Client: newInterceptClient(func(req *http.Request) (*http.Response, error) {
			gotPath = req.URL.Path
			return cannedResponse(req, http.StatusOK, releaseJSON()), nil
		}),
	}
	// No leading "v" — ByTag should prepend it.
	if _, err := c.ByTag(context.Background(), "8.8.8"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(gotPath, "/tags/v8.8.8") {
		t.Errorf("path = %q, want suffix /tags/v8.8.8", gotPath)
	}
}

func TestCheckerFetchErrors(t *testing.T) {
	tests := []struct {
		name string
		code int
		body string
		trFn roundTripFunc
	}{
		{
			name: "not found",
			code: http.StatusNotFound, body: "{}",
		},
		{
			name: "forbidden",
			code: http.StatusForbidden, body: "{}",
		},
		{
			name: "rate limited 429",
			code: 429, body: "{}",
		},
		{
			name: "server error 500",
			code: http.StatusInternalServerError, body: "{}",
		},
		{
			name: "transport error",
			trFn: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("boom")
			},
		},
		{
			name: "bad json",
			code: http.StatusOK, body: "{not-json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trFn := tt.trFn
			if trFn == nil {
				code, body := tt.code, tt.body
				trFn = func(req *http.Request) (*http.Response, error) {
					return cannedResponse(req, code, body), nil
				}
			}
			c := &Checker{Repo: "o/r", Client: newInterceptClient(trFn)}
			if _, err := c.Latest(context.Background()); err == nil {
				t.Fatalf("%s: expected error, got nil", tt.name)
			}
		})
	}
}
