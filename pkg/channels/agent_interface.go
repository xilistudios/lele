package channels

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
