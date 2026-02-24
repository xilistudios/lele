package channels

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// ApprovalManager gestiona comandos pendientes de aprobación
type ApprovalManager struct {
	pending map[string]*PendingApproval // map[approvalID]*PendingApproval
	mu      sync.RWMutex
	timeout time.Duration
}

// PendingApproval representa una solicitud de aprobación pendiente
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

// NewApprovalManager crea un nuevo gestor de aprobaciones
func NewApprovalManager() *ApprovalManager {
	return &ApprovalManager{
		pending: make(map[string]*PendingApproval),
		timeout: 5 * time.Minute,
	}
}

// generateID genera un ID único para la aprobación
func generateID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), rand.Intn(10000))
}

// CreateApproval crea una nueva solicitud de aprobación
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

	// Configurar timeout automático
	go func() {
		time.Sleep(am.timeout)
		am.mu.Lock()
		if p, ok := am.pending[approval.ID]; ok {
			delete(am.pending, approval.ID)
			am.mu.Unlock()
			// Notificar rechazo por timeout
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

// GetApproval obtiene una aprobación pendiente por ID
func (am *ApprovalManager) GetApproval(approvalID string) *PendingApproval {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.pending[approvalID]
}

// HandleApproval procesa la respuesta del usuario (approve/reject)
// Returns the approval that was handled and any error
func (am *ApprovalManager) HandleApproval(approvalID string, approved bool) (*PendingApproval, error) {
	am.mu.Lock()
	approval, ok := am.pending[approvalID]
	if !ok {
		am.mu.Unlock()
		return nil, fmt.Errorf("approval not found: %s", approvalID)
	}
	delete(am.pending, approvalID)
	am.mu.Unlock()

	// Enviar respuesta al canal
	select {
	case approval.responseChan <- approved:
	default:
	}

	// Ejecutar callback correspondiente
	if approved && approval.OnApproved != nil {
		approval.OnApproved()
	} else if !approved && approval.OnRejected != nil {
		approval.OnRejected()
	}

	return approval, nil
}

// WaitForResponse espera la respuesta del usuario con timeout
func (p *PendingApproval) WaitForResponse(timeout time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	select {
	case approved := <-p.responseChan:
		return approved, nil
	case <-ctx.Done():
		return false, fmt.Errorf("approval timeout")
	}
}

// cleanupExpired limpia aprobaciones expiradas
func (am *ApprovalManager) cleanupExpired() {
	am.mu.Lock()
	defer am.mu.Unlock()

	now := time.Now()
	for id, approval := range am.pending {
		if now.Sub(approval.CreatedAt) > am.timeout {
			delete(am.pending, id)
			// Notificar timeout
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

// BuildApprovalKeyboard crea el teclado inline para Telegram
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

// GetTimeout returns the current timeout value
func (am *ApprovalManager) GetTimeout() time.Duration {
	return am.timeout
}

// SetTimeout configura el tiempo de espera para aprobaciones
func (am *ApprovalManager) SetTimeout(timeout time.Duration) {
	am.timeout = timeout
}
