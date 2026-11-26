package channels

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

var (
	ErrApprovalNotFound          = errors.New("approval not found")
	ErrApprovalSessionMismatch   = errors.New("approval session mismatch")
	ErrApprovalRequesterMismatch = errors.New("approval requester mismatch")
)

var conversationAliasSuffix = regexp.MustCompile(`:chat:\d+$`)

func approvalSessionMatches(approvalKey, callerKey string) bool {
	if approvalKey == "" || callerKey == "" {
		return false
	}
	if approvalKey == callerKey {
		return true
	}
	a := conversationAliasSuffix.ReplaceAllString(approvalKey, "")
	b := conversationAliasSuffix.ReplaceAllString(callerKey, "")
	return a == b
}

func requesterIDPart(requesterID string) string {
	if idx := strings.Index(requesterID, "|"); idx >= 0 {
		return requesterID[:idx]
	}
	return requesterID
}

func approvalRequesterMatches(approvalRequesterID, callerRequesterID string) bool {
	if approvalRequesterID == "" || callerRequesterID == "" {
		return false
	}
	return requesterIDPart(approvalRequesterID) == requesterIDPart(callerRequesterID)
}

type approvalAuthority struct {
	sessionKey       string
	requesterID      string
	requireRequester bool
}

func (a *approvalAuthority) requiresRequesterMatch(approval *PendingApproval) bool {
	return a.requireRequester && approval.RequesterID != ""
}

type ApprovalManager struct {
	pending map[string]*PendingApproval
	mu      sync.RWMutex
	timeout time.Duration
}

type PendingApproval struct {
	ID           string
	SessionKey   string
	RequesterID  string
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

func (am *ApprovalManager) CreateApproval(sessionKey, command, reason string, chatID int64, requesterID string) *PendingApproval {
	am.cleanupExpired()

	approval := &PendingApproval{
		ID:           generateID(),
		SessionKey:   sessionKey,
		RequesterID:  requesterID,
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
	return am.resolveApproval(approvalID, approved, nil)
}

func (am *ApprovalManager) HandleApprovalForSession(approvalID string, approved bool, callerSessionKey string) (*PendingApproval, error) {
	return am.resolveApproval(approvalID, approved, &approvalAuthority{sessionKey: callerSessionKey})
}

func (am *ApprovalManager) HandleApprovalForRequester(approvalID string, approved bool, callerSessionKey, callerRequesterID string) (*PendingApproval, error) {
	return am.resolveApproval(approvalID, approved, &approvalAuthority{
		sessionKey:       callerSessionKey,
		requesterID:      callerRequesterID,
		requireRequester: true,
	})
}

func (am *ApprovalManager) CheckApprovalForRequester(approvalID, callerSessionKey, callerRequesterID string) (*PendingApproval, error) {
	am.mu.RLock()
	approval, ok := am.pending[approvalID]
	am.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrApprovalNotFound, approvalID)
	}
	authority := &approvalAuthority{sessionKey: callerSessionKey, requesterID: callerRequesterID, requireRequester: true}
	if err := authorizeApproval(approval, authority); err != nil {
		return nil, err
	}
	return approval, nil
}

func authorizeApproval(approval *PendingApproval, authority *approvalAuthority) error {
	if authority == nil {
		return nil
	}
	if !approvalSessionMatches(approval.SessionKey, authority.sessionKey) {
		return ErrApprovalSessionMismatch
	}
	if authority.requiresRequesterMatch(approval) && !approvalRequesterMatches(approval.RequesterID, authority.requesterID) {
		return ErrApprovalRequesterMismatch
	}
	return nil
}

func (am *ApprovalManager) resolveApproval(approvalID string, approved bool, authority *approvalAuthority) (*PendingApproval, error) {
	am.mu.Lock()
	approval, ok := am.pending[approvalID]
	if !ok {
		am.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrApprovalNotFound, approvalID)
	}
	if err := authorizeApproval(approval, authority); err != nil {
		am.mu.Unlock()
		return nil, err
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
