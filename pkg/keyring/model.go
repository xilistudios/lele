package keyring

import "time"

type Secret struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Value       string    `json:"value"`
	Tags        []string  `json:"tags"`
	Scope       []string  `json:"scope"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	CreatedBy   string    `json:"created_by"`
}

func (s *Secret) Meta() SecretMeta {
	return SecretMeta{
		Name:        s.Name,
		Description: s.Description,
		Tags:        s.Tags,
		Scope:       s.Scope,
		CreatedAt:   s.CreatedAt,
		UpdatedAt:   s.UpdatedAt,
		CreatedBy:   s.CreatedBy,
	}
}

func (s *Secret) HasScope() bool {
	return len(s.Scope) > 0
}

func (s *Secret) AllowsAgent(agentID string) bool {
	if len(s.Scope) == 0 {
		return true
	}
	for _, id := range s.Scope {
		if id == agentID {
			return true
		}
	}
	return false
}

type SecretMeta struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Tags        []string  `json:"tags"`
	Scope       []string  `json:"scope"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	CreatedBy   string    `json:"created_by"`
}

type AccessRecord struct {
	SecretName string    `json:"secret_name"`
	AgentID    string    `json:"agent_id"`
	SessionKey string    `json:"session_key"`
	Action     string    `json:"action"`
	Timestamp  time.Time `json:"timestamp"`
	Granted    bool      `json:"granted"`
}
