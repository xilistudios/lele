package skills

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadLimitedBody_ExceedsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		big := strings.Repeat("x", MaxSkillBodyBytes+1)
		w.Write([]byte(big))
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	_, err = readLimitedBody(resp)
	if err == nil {
		t.Fatal("expected error for body exceeding MaxSkillBodyBytes, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error should mention 'exceeds', got: %v", err)
	}
}

func TestReadLimitedBody_ExactlyAtLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("y", MaxSkillBodyBytes)))
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	data, err := readLimitedBody(resp)
	if err != nil {
		t.Fatalf("body of exactly MaxSkillBodyBytes should pass, got error: %v", err)
	}
	if len(data) != MaxSkillBodyBytes {
		t.Fatalf("expected %d bytes, got %d", MaxSkillBodyBytes, len(data))
	}
}

func TestReadLimitedBody_EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	data, err := readLimitedBody(resp)
	if err != nil {
		t.Fatalf("empty body should not error, got: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("expected 0 bytes, got %d", len(data))
	}
}
