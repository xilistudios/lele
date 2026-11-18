package channels

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// A failed LLM turn is persisted as an assistant message carrying ErrorDetail
// (UI-only) with ExcludeFromContext=true. GET /api/v1/chat/history is how the
// WebUI learns about it after a refetch, and chatHistoryBuilder.message is the
// single construction site for both the resident window and the evicted pages,
// so the field must survive there and stay off the wire when it is empty.

func TestChatHistoryBuilder_CarriesErrorDetail(t *testing.T) {
	builder := newChatHistoryBuilder(nil)

	failure := builder.message("id-failure", providers.Message{
		Role:               "assistant",
		Content:            "❌ Error processing message: LLM call failed after retries",
		ErrorDetail:        "LLM call failed after retries: 429 Too Many Requests",
		ExcludeFromContext: true,
	})

	if failure.ErrorDetail != "LLM call failed after retries: 429 Too Many Requests" {
		t.Fatalf("ErrorDetail not carried into the history DTO: %+v", failure)
	}
	if !failure.ExcludeFromContext {
		t.Error("the failure message must stay flagged as excluded from context")
	}

	data, err := json.Marshal(failure)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"error_detail"`) {
		t.Fatalf("error_detail missing from the history JSON: %s", data)
	}

	// A normal assistant message must not grow the key (omitempty contract).
	normal := builder.message("id-normal", providers.Message{Role: "assistant", Content: "hi"})
	if normal.ErrorDetail != "" {
		t.Fatalf("normal message gained an ErrorDetail: %+v", normal)
	}
	data, err = json.Marshal(normal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "error_detail") {
		t.Fatalf("normal message must omit error_detail: %s", data)
	}
}
