package channels

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPendingApprovalWaitForResponse_RespectsContextCancellation(t *testing.T) {
	approval := &PendingApproval{
		responseChan: make(chan bool, 1),
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := approval.WaitForResponse(ctx, time.Minute)
		errCh <- err
	}()

	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("wait did not stop after context cancellation")
	}
}

// TestGenerateID_Uniqueness generates 1000 IDs and asserts they are all unique.
func TestGenerateID_Uniqueness(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := generateID()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID generated at iteration %d: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

// TestGenerateID_LengthAndEntropy asserts the ID is at least 32 hex characters
// (128 bits) and is valid hex.
func TestGenerateID_LengthAndEntropy(t *testing.T) {
	id := generateID()
	if len(id) < 32 {
		t.Fatalf("ID too short: len=%d, want >= 32; id=%q", len(id), id)
	}
	// Must be valid hex.
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatalf("ID is not valid hex: %q, err=%v", id, err)
	}
}

// TestGenerateID_NoMathRand verifies that approval_manager.go no longer
// imports math/rand (security requirement — the approval ID is a capability
// token and must use crypto/rand).
func TestGenerateID_NoMathRand(t *testing.T) {
	// Read the source file and check for math/rand import.
	// This is a compile-time invariant enforced by code review, but we
	// duplicate it as a test so CI catches regressions.
	// The test runs from the package directory, so the path is relative.
	imports, err := readImportsFromFile("approval_manager.go")
	if err != nil {
		t.Skipf("could not read source: %v", err)
	}
	for _, imp := range imports {
		if imp == "math/rand" {
			t.Fatal("approval_manager.go still imports math/rand; must use crypto/rand")
		}
	}
}

// readImportsFromFile is a tiny helper that reads the import block of a Go
// source file and returns the imported paths. Used only by tests.
func readImportsFromFile(path string) ([]string, error) {
	data, err := readFileBytes(path)
	if err != nil {
		return nil, err
	}
	var imports []string
	inImport := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "import (" {
			inImport = true
			continue
		}
		if inImport && trimmed == ")" {
			break
		}
		if inImport && trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			// Strip quotes and optional alias.
			trimmed = strings.Trim(trimmed, "\"")
			if idx := strings.LastIndex(trimmed, " "); idx >= 0 {
				trimmed = strings.Trim(trimmed[idx+1:], "\"")
			}
			imports = append(imports, trimmed)
		}
	}
	return imports, nil
}

func readFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}
