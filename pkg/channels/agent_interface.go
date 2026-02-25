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
	// ClearCooldowns limpia todos los cooldowns de proveedores LLM
	ClearCooldowns()
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
