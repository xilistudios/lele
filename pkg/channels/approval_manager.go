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

// conversationAliasSuffix matches the conversation-rotation suffix the agent
// loop appends on /new and /agent (`base:chat:N`). Approval session checks must
// tolerate it: the approval stores the RESOLVED key while clients keep using
// the BASE key, and both refer to the same conversation. Mirrors the frontend's
// stripConversationAlias (web/src/hooks/event-handlers/helpers.ts).
var conversationAliasSuffix = regexp.MustCompile(`:chat:\d+$`)

// approvalSessionMatches reports whether a caller session key is allowed to
// resolve an approval created under approvalKey. Empty keys mean a legacy/local
// caller and always match. Base and aliased conversation keys (`base` vs
// `base:chat:N`) are considered the same session, mirroring the frontend's
// sessionKeysLooselyMatch.
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

// generateID produces a cryptographically random approval ID (128 bits, hex).
// The approval ID is a capability token — possessing it authorises a response —
// so it must not be guessable. crypto/rand is used throughout; there is no
// fallback to math/rand.
func generateID() string {
	b := make([]byte, 16) // 128 bits
	if _, err := rand.Read(b); err != nil {
		panic("approval: crypto/rand.Read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
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

// HandleApproval procesa la respuesta del usuario (approve/reject) sin
// verificación de sesión. Legado: solo para llamadores de confianza local
// (p.ej. la TUI monousuario) o tests. Las superficies de red DEBEN usar
// HandleApprovalForSession.
// Returns the approval that was handled and any error
func (am *ApprovalManager) HandleApproval(approvalID string, approved bool) (*PendingApproval, error) {
	return am.HandleApprovalForSession(approvalID, approved, "")
}

// HandleApprovalForSession procesa la respuesta del usuario verificando que la
// sesión que responde coincida con la sesión que originó la aprobación.
//
// callerSessionKey es la sesión autenticada del llamador (p.ej. client.SessionKey
// en WebSocket, la sesión ya validada por validateSessionOwnership en REST, o
// "telegram:<chatID>" para callbacks de Telegram). Un callerSessionKey vacío se
// tolera como llamador legado/confiable-local, pero los puntos de entrada de red
// SIEMPRE deben pasar una clave no vacía: resolver una aprobación de otra sesión
// permitiría a un cliente autenticado aprobar ejecuciones que nunca vio,
// saltándose los patrones de denegación (TUI-H2).
//
// En caso de discrepancia de sesión NO se elimina la aprobación (sigue
// pendiente para el dueño legítimo) y se devuelve error.
// Returns the approval that was handled and any error
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

// WaitForResponse espera la respuesta del usuario con timeout y respeta la cancelación del contexto.
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
