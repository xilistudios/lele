package providers

import (
	"context"
	"fmt"

	json "encoding/json"

	copilot "github.com/github/copilot-sdk/go"
)

type GitHubCopilotProvider struct {
	uri         string
	connectMode string

	client  *copilot.Client
	session *copilot.Session
}

func NewGitHubCopilotProvider(uri string, connectMode string, model string) (*GitHubCopilotProvider, error) {

	var session *copilot.Session
	var client *copilot.Client
	if connectMode == "" {
		connectMode = "grpc"
	}
	switch connectMode {

	case "stdio":
	case "grpc":
		client = copilot.NewClient(&copilot.ClientOptions{
			CLIUrl: uri,
		})
		if err := client.Start(context.Background()); err != nil {
			return nil, fmt.Errorf("Can't connect to Github Copilot, https://github.com/github/copilot-sdk/blob/main/docs/getting-started.md#connecting-to-an-external-cli-server for details")
		}
		var err error
		session, err = client.CreateSession(context.Background(), &copilot.SessionConfig{
			Model: model,
			Hooks: &copilot.SessionHooks{},
		})
		if err != nil {
			client.Stop()
			return nil, fmt.Errorf("copilot create session: %w", err)
		}

	}

	return &GitHubCopilotProvider{
		uri:         uri,
		connectMode: connectMode,
		client:      client,
		session:     session,
	}, nil
}

func (p *GitHubCopilotProvider) Close() {
	if p.client != nil {
		p.client.Stop()
	}
}

func (p *GitHubCopilotProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	if p.session == nil {
		return nil, fmt.Errorf("copilot session not initialized")
	}

	type tempMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	out := make([]tempMessage, 0, len(messages))

	for _, msg := range messages {
		out = append(out, tempMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	fullcontent, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshal messages: %w", err)
	}

	content, err := p.session.Send(ctx, copilot.MessageOptions{
		Prompt: string(fullcontent),
	})
	if err != nil {
		return nil, fmt.Errorf("copilot send: %w", err)
	}

	return &LLMResponse{
		FinishReason: "stop",
		Content:      content,
	}, nil

}

func (p *GitHubCopilotProvider) GetDefaultModel() string {

	return "gpt-4.1"
}
