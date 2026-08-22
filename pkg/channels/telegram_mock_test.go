package channels

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/mymmrac/telego"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// testTelegramBotToken is a syntactically valid Telegram bot token accepted by
// telego.NewBot. Requests are intercepted by a mock API server so no real
// network traffic occurs.
const testTelegramBotToken = "1234567890:aaaabbbbaaaabbbbaaaabbbbaaaabbbbccc"

// mockTelegramAPI emulates the Telegram Bot API using an httptest server. Each
// method is routed by the last URL path segment and answers with a successful
// JSON response of the appropriate shape.
type mockTelegramAPI struct {
	server   *httptest.Server
	bot      *telego.Bot
	requests []string // captured "method:body" strings
	mu       sync.Mutex
	// getFilePath overrides the file_path returned by getFile ("" => error).
	getFileErr bool
}

func newMockTelegramAPI() *mockTelegramAPI {
	m := &mockTelegramAPI{}
	m.server = httptest.NewServer(http.HandlerFunc(m.handle))
	m.bot, _ = telego.NewBot(testTelegramBotToken,
		telego.WithAPIServer(m.server.URL),
		telego.WithDiscardLogger(),
	)
	return m
}

func (m *mockTelegramAPI) Close() {
	if m.server != nil {
		m.server.Close()
	}
}

func (m *mockTelegramAPI) record(method, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, method+":"+body)
}

func (m *mockTelegramAPI) hadMethod(method string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.requests {
		if strings.HasPrefix(r, method+":") {
			return true
		}
	}
	return false
}

func (m *mockTelegramAPI) handle(w http.ResponseWriter, r *http.Request) {
	// Extracts method name from URL: /bot<token>/test/<method> or /bot<token>/<method>
	segments := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	method := segments[len(segments)-1]

	m.record(method, r.URL.RawQuery)

	w.Header().Set("Content-Type", "application/json")
	switch method {
	case "sendMessage", "editMessageText", "editMessageReplyMarkup":
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":777,"date":0,"chat":{"id":1,"type":"private"},"text":"ok"}}`)
	case "answerCallbackQuery", "sendChatAction", "setMyCommands", "deleteWebhook", "setWebhook":
		fmt.Fprint(w, `{"ok":true,"result":true}`)
	case "getFile":
		if m.getFileErr {
			fmt.Fprint(w, `{"ok":false,"error_code":400,"description":"file not found"}`)
			return
		}
		fmt.Fprint(w, `{"ok":true,"result":{"file_id":"F","file_path":"downloads/file.jpg"}}`)
	case "getUpdates":
		fmt.Fprint(w, `{"ok":true,"result":[]}`)
	case "getMe":
		fmt.Fprint(w, `{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"Test","username":"testbot"}}`)
	default:
		http.Error(w, "unknown method "+method, 500)
	}
}

// newMockTelegramChannel builds a TelegramChannel wired to a mock Telegram API
// server. The bus, agentLoop and approvalManager may be nil (defaults apply).
func newMockTelegramChannel(t interface {
	Helper()
	Fatalf(format string, args ...interface{})
}, msgBus *bus.MessageBus, agentLoop AgentProvidable, approvalManager *ApprovalManager) (*TelegramChannel, *mockTelegramAPI) {
	server := newMockTelegramAPI()
	cfg := config.DefaultConfig()
	cfg.Channels.Telegram.Enabled = true
	cfg.Channels.Telegram.Token = testTelegramBotToken
	cfg.Agents.Defaults.Model = "gpt-4"
	cfg.Agents.Defaults.Provider = "openai"

	mb := msgBus
	if mb == nil {
		mb = bus.NewMessageBus()
	}

	ch := &TelegramChannel{
		BaseChannel:     NewBaseChannel("telegram", nil, mb, nil),
		config:          cfg,
		bot:             server.bot,
		commands:        NewTelegramCommands(server.bot, cfg, agentLoop),
		chatIDs:         make(map[string]int64),
		placeholders:    sync.Map{},
		stopThinking:    sync.Map{},
		agentLoop:       agentLoop,
		approvalManager: approvalManager,
		processedIDs:    make(map[string]struct{}),
	}
	return ch, server
}
