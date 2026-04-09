package channels

import (
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

// AgentSessionManager define la interfaz necesaria para gestionar agentes por sesión
// Esta interfaz es implementada por agent.AgentLoop para evitar ciclos de importación
type AgentSessionManager interface {
	GetSessionAgent(sessionKey string) string
	SetSessionAgent(sessionKey, agentID string)
	ListAvailableAgentIDs() []string
	GetDefaultAgentID() string
}

// AgentProvidable extiende la interfaz con métodos para obtener información de agentes
type AgentProvidable interface {
	AgentSessionManager
	// GetAgentInfo devuelve información básica de un agente
	GetAgentInfo(agentID string) (AgentBasicInfo, bool)
	// GetSessionHistory devuelve el historial persistido de una sesión
	GetSessionHistory(sessionKey string) []providers.Message
	// GetSessionModel devuelve el modelo efectivo de una sesión
	GetSessionModel(sessionKey string) string
	// SetSessionModel establece el modelo de una sesión
	SetSessionModel(sessionKey, model string) string
	// ListAvailableModels devuelve los modelos configurados para un agente/sesión
	ListAvailableModels(agentID string) []string
	// GetConfigSnapshot devuelve la configuración actual
	GetConfigSnapshot() *config.Config
	// GetStatus devuelve el estado actual del agente para una sesión
	GetStatus(sessionKey string) string
	// StopAgent detiene el procesamiento del agente para una sesión
	StopAgent(sessionKey string) string
	// CompactSession compacta la sesión para ahorrar tokens
	CompactSession(sessionKey string) string
	// ToggleVerbose cambia el modo verbose para una sesión
	ToggleVerbose(sessionKey string) string
	// GetVerboseLevel devuelve el nivel de verbose actual para una sesión
	GetVerboseLevel(sessionKey string) string
	// SetVerboseLevel establece el nivel de verbose para una sesión
	SetVerboseLevel(sessionKey string, level string) bool
	// GetThinkLevel devuelve el nivel de razonamiento actual para una sesión
	GetThinkLevel(sessionKey string) string
	// SetThinkLevel establece el nivel de razonamiento para una sesión
	SetThinkLevel(sessionKey string, level string) bool
	// GetSubagents list los subagentes activos
	GetSubagents() string
	// ClearSession limpia el historial de una sesión
	ClearSession(sessionKey string) string
	// GetName devuelve el nombre de una sesión
	GetName(sessionKey string) string
	// SetName establece el nombre de una sesión
	SetName(sessionKey string, name string) error
	// ResolveSessionKey resuelve el alias de session_key si existe
	ResolveSessionKey(sessionKey string) string
	// IsSessionProcessing devuelve true si hay un procesamiento LLM activo para la sesión
	IsSessionProcessing(sessionKey string) bool
}

// AgentBasicInfo contiene información pública de un agente
type AgentBasicInfo struct {
	ID            string
	Name          string
	Model         string
	Workspace     string
	MaxIterations int
	MaxTokens     int
	Temperature   float64
	Fallbacks     []string
	SkillsFilter  []string
}
