package channels

import (
	"context"
	"errors"
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
