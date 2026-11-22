package tools

import (
	"encoding/json"

	"github.com/xilistudios/lele/pkg/providers"
)

type ToolResult struct {
	ForLLM string `json:"for_llm"`

	ForUser string `json:"for_user,omitempty"`

	Silent bool `json:"silent"`

	IsError bool `json:"is_error"`

	Async bool `json:"async"`

	Err error `json:"-"`

	Metadata map[string]string `json:"metadata,omitempty"`

	ContextMessages []providers.Message `json:"-"`

	ApprovalRequired *ApprovalInfo `json:"approval_required,omitempty"`
}

func NewToolResult(forLLM string) *ToolResult {
	return &ToolResult{
		ForLLM: forLLM,
	}
}

func SilentResult(forLLM string) *ToolResult {
	return &ToolResult{
		ForLLM:  forLLM,
		Silent:  true,
		IsError: false,
		Async:   false,
	}
}

func AsyncResult(forLLM string) *ToolResult {
	return &ToolResult{
		ForLLM:  forLLM,
		Silent:  false,
		IsError: false,
		Async:   true,
	}
}

func ErrorResult(message string) *ToolResult {
	return &ToolResult{
		ForLLM:  message,
		Silent:  false,
		IsError: true,
		Async:   false,
	}
}

func UserResult(content string) *ToolResult {
	return &ToolResult{
		ForLLM:  content,
		ForUser: content,
		Silent:  false,
		IsError: false,
		Async:   false,
	}
}

func (tr *ToolResult) MarshalJSON() ([]byte, error) {
	type Alias ToolResult
	return json.Marshal(&struct {
		*Alias
	}{
		Alias: (*Alias)(tr),
	})
}

func (tr *ToolResult) WithError(err error) *ToolResult {
	tr.Err = err
	return tr
}
