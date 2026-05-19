package channels

// REST HTTP handlers for the Native channel API.
//
// To keep this package manageable, the handlers are split across multiple files
// organized by domain:
//
//   - rest_auth.go    — Authentication endpoints (PIN, pair, refresh, status)
//   - rest_chat.go    — Chat endpoints (send, history, sessions, approve)
//   - rest_session.go — Session configuration (model, agent, thinking, name)
//   - rest_agent.go   — Agent management (info, files, workspace)
//   - rest_config.go  — Configuration endpoints (get, put, validate)
//   - rest_system.go  — System endpoints (tools, models, skills, status, providers)
//   - rest_stream.go  — Server-Sent Events streaming
