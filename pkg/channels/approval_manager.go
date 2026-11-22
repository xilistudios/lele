package channels

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

var conversationAliasSuffix = regexp.MustCompile(`:chat:\d+$`)

func approvalSessionMatches(approvalKey, callerKey string) bool {
	if approvalKey == "" || callerKey == "" {
		return true
	}
	if approvalKey == callerKey {
		return true
	}
	a := conversationAliasSuffix.ReplaceAllString(approvalKey, "")
	b := conversationAliasSuffix.ReplaceAllString(callerKey, "")
	return a == b
}

type ApprovalManager struct {
	pending map[string]*PendingApproval
	mu      sync.RWMutex
	timeout time.Duration
}

type PendingApproval struct {
	ID           string
	SessionKey   string
	Command      string
	Reason       string
	ChatID       int64
	MessageID    int
	CreatedAt    time.Time
	OnApproved   func()
	OnRejected   func()
	responseChan chan bool
}

func NewApprovalManager() *ApprovalManager {
	return &ApprovalManager{
		pending: make(map[string]*PendingApproval),
		timeout: 5 * time.Minute,
	}
}

func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("approval: crypto/rand.Read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func (am *ApprovalManager) CreateApproval(sessionKey, command, reason string, chatID int64) *PendingApproval {
	am.cleanupExpired()

	approval := &PendingApproval{
		ID:           generateID(),
		SessionKey:   sessionKey,
		Command:      command,
		Reason:       reason,
		ChatID:       chatID,
		CreatedAt:    time.Now(),
		responseChan: make(chan bool, 1),
	}

	am.mu.Lock()
	am.pending[approval.ID] = approval
	am.mu.Unlock()

	go func() {
		time.Sleep(am.timeout)
		am.mu.Lock()
		if p, ok := am.pending[approval.ID]; ok {
			delete(am.pending, approval.ID)
			am.mu.Unlock()
			select {
			case p.responseChan <- false:
			default:
			}
			if p.OnRejected != nil {
				p.OnRejected()
			}
		} else {
			am.mu.Unlock()
		}
	}()

	return approval
}

func (am *ApprovalManager) GetApproval(approvalID string) *PendingApproval {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.pending[approvalID]
}

func (am *ApprovalManager) HandleApproval(approvalID string, approved bool) (*PendingApproval, error) {
	return am.HandleApprovalForSession(approvalID, approved, "")
}

func (am *ApprovalManager) HandleApprovalForSession(approvalID string, approved bool, callerSessionKey string) (*PendingApproval, error) {
	am.mu.Lock()
	approval, ok := am.pending[approvalID]
	if !ok {
		am.mu.Unlock()
		return nil, fmt.Errorf("approval not found: %s", approvalID)
	}
	if !approvalSessionMatches(approval.SessionKey, callerSessionKey) {
		am.mu.Unlock()
		return nil, fmt.Errorf("approval session mismatch")
	}
	delete(am.pending, approvalID)
	am.mu.Unlock()

	select {
	case approval.responseChan <- approved:
	default:
	}

	if approved && approval.OnApproved != nil {
		approval.OnApproved()
	} else if !approved && approval.OnRejected != nil {
		approval.OnRejected()
	}

	return approval, nil
}

func (p *PendingApproval) WaitForResponse(ctx context.Context, timeout time.Duration) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case approved := <-p.responseChan:
		return approved, nil
	case <-waitCtx.Done():
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return false, fmt.Errorf("approval timeout")
	}
}

func (am *ApprovalManager) cleanupExpired() {
	am.mu.Lock()
	defer am.mu.Unlock()

	now := time.Now()
	for id, approval := range am.pending {
		if now.Sub(approval.CreatedAt) > am.timeout {
			delete(am.pending, id)
			select {
			case approval.responseChan <- false:
			default:
			}
			if approval.OnRejected != nil {
				approval.OnRejected()
			}
		}
	}
}

func (am *ApprovalManager) BuildApprovalKeyboard(approvalID string) *telego.InlineKeyboardMarkup {
	return tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("✅ Ejecutar").WithCallbackData(fmt.Sprintf("approval:approve:%s", approvalID)),
			tu.InlineKeyboardButton("❌ Cancelar").WithCallbackData(fmt.Sprintf("approval:reject:%s", approvalID)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("🔍 Ver comando completo").WithCallbackData(fmt.Sprintf("approval:view:%s", approvalID)),
		),
	)
}

func (am *ApprovalManager) GetTimeout() time.Duration {
	return am.timeout
}

func (am *ApprovalManager) SetTimeout(timeout time.Duration) {
	am.timeout = timeout
}
