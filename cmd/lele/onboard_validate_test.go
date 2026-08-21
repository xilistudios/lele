package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestValidateProvider_LocalhostShort returns true for localhost without any
// HTTP call, covering the early-return short-circuit branch.
func TestValidateProvider_LocalhostShort(t *testing.T) {
	if !validateProvider("test", "key", "localhost", "Bearer") {
		t.Error("expected true for bare 'localhost' api base")
	}
	if !validateProvider("test", "key", "http://localhost", "Bearer") {
		t.Error("expected true for http://localhost api base")
	}
}

func TestValidateProvider_EmptyKeyOrBase(t *testing.T) {
	if validateProvider("test", "", "http://localhost", "Bearer") {
		t.Error("expected false when api key is empty")
	}
	if validateProvider("test", "key", "", "Bearer") {
		t.Error("expected false when api base is empty")
	}
}

func TestValidateProvider_Http200_Bearer(t *testing.T) {
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate auth header style.
		if r.Header.Get("Authorization") != "Bearer abc123" {
			t.Errorf("unexpected Authorization header: %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer svr.Close()

	if !validateProvider("test", "abc123", svr.URL, "Bearer") {
		t.Error("expected true for 200 response")
	}
}

func TestValidateProvider_Http200_XAPIKey(t *testing.T) {
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "xyz987" {
			t.Errorf("unexpected X-Api-Key header: %q", r.Header.Get("X-Api-Key"))
		}
		if r.URL.Path != "/models" {
			t.Errorf("expected /models path, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer svr.Close()

	if !validateProvider("test", "xyz987", svr.URL, "x-api-key") {
		t.Error("expected true for 200 response with x-api-key")
	}
}

func TestValidateProvider_Http403(t *testing.T) {
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer svr.Close()

	// 403 is treated as valid (the model list is restricted but reachable).
	if !validateProvider("test", "key", svr.URL, "Bearer") {
		t.Error("expected true for 403 response")
	}
}

func TestValidateProvider_Http500(t *testing.T) {
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer svr.Close()

	if validateProvider("test", "key", svr.URL, "Bearer") {
		t.Error("expected false for 500 response")
	}
}

func TestValidateProvider_Unreachable(t *testing.T) {
	// A port that is very unlikely to be listening.
	if validateProvider("test", "key", "http://127.0.0.1:1", "Bearer") {
		t.Error("expected false when server unreachable")
	}
}