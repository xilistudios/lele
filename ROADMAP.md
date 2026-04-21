# 🦐 Lele Roadmap

> **Vision**: Build the ultimate lightweight, secure, and fully autonomous AI Agent infrastructure. Automate the mundane, unleash your creativity.

---

## 📋 Phases Index

| Phase | Name | Status | Priority |
|-------|------|--------|----------|
| **Phase 1** | Communication Channels Stabilization | 🔴 Planned | 🔥 Critical |
| **Phase 2** | Stable & Fully Configurable WebUI | 🔴 Planned | 🔥 Critical |
| **Phase 3** | Comprehensive E2E Test Suite | ⚪ Pending | High |
| **Phase 4** | Release System (Stable/Rolling) | ⚪ Pending | High |
| **Phase 5** | MCP Support | ⚪ Pending | Medium |
| **Phase 6** | Memory System with RAG | ⚪ Pending | Medium |
| **Phase 7** | Lele Desktop Client | ⚪ Pending | Future |

---

## 🔌 PHASE 1: Communication Channels Stabilization

**Goal**: All communication channels must be stable, robust, and fully functional.

### 1.1 Native Channel (Top Priority)
- [ ] Stabilize `pkg/channels/native.go` — main communication interface
- [ ] Complete tests in `pkg/channels/native_api_test.go`
- [ ] Document native channel API
- [ ] Implement robust error handling and retries
- [ ] Support for response streaming
- [ ] Implement authentication and authorization for native channel

### 1.2 IM Channels (Messaging Infrastructure)
- [ ] **Telegram** (currently most mature)
  - [ ] Stabilize `telegram.go` and related modules
  - [ ] Complete test coverage in `telegram_*_test.go`
  - [ ] Validate approval system (`telegram_approval.go`)
  - [ ] Stabilize message formatting (`telegram_formatting.go`)
  - [ ] Transport testing (`telegram_transport.go`)

- [ ] **Discord**
  - [ ] Stabilize `discord.go`
  - [ ] Add unit tests
  - [ ] Full attachments support
  - [ ] Slash commands integration

- [ ] **WhatsApp**
  - [ ] Stabilize `whatsapp.go`
  - [ ] Add unit tests
  - [ ] Full multimedia support
  - [ ] Session handling

- [ ] **Slack**
  - [ ] Stabilize `slack.go`
  - [ ] Complete tests (`slack_test.go`)
  - [ ] Blocks and attachments support
  - [ ] Robust event subscriptions

- [ ] **Other channels** (OneBot, QQ, LINE, DingTalk, Feishu, etc.)
  - [ ] Audit current state
  - [ ] Stabilize actively used channels
  - [ ] Document status of each channel

### 1.3 WebSocket Channel
- [ ] Stabilize `websocket.go`
- [ ] Support for automatic reconnection
- [ ] Heartbeat/ping-pong handling
- [ ] Bidirectional messaging support

### 1.4 Base Channel System
- [ ] Stabilize `base.go` — base interface for all channels
- [ ] Improve `manager.go` — multi-channel management
- [ ] Stabilize `types.go` — shared types
- [ ] Implement centralized error logging system
- [ ] Robust rate limiting system (`ratelimit.go`)
- [ ] File upload system (`upload.go`)
- [ ] Output chunking (`outbound_chunking.go`)

### 1.5 Phase 1 Success Criteria
- [ ] All active channels pass unit tests with >80% coverage
- [ ] Integration tests for critical flows
- [ ] Updated documentation for each channel
- [ ] No known crashes in production
- [ ] Response time < 2s for simple messages

---

## 🖥️ PHASE 2: Stable & 100% Configurable WebUI

**Goal**: A complete, stable web interface that is fully configurable without the need to manually edit configuration files.

### 2.1 WebUI Stabilization
- [ ] Audit current state of `web/`
- [ ] Stabilize frontend (framework, build, dependencies)
- [ ] Stabilize WebUI backend API
- [ ] Test all web API endpoints
- [ ] Error handling in UI (loading, error, empty states)

### 2.2 Full Configuration from WebUI
- [ ] **Provider Management**
  - [ ] Full CRUD for providers from UI
  - [ ] Connection testing to providers
  - [ ] Available models configuration
  - [ ] Default model selection

- [ ] **Channel Management**
  - [ ] Enable/disable channels from UI
  - [ ] Channel credentials configuration
  - [ ] Configuration preview before applying
  - [ ] Channel connection testing

- [ ] **Agent Management**
  - [ ] Agent configuration from UI
  - [ ] Subagent management
  - [ ] Available tools configuration
  - [ ] Active session management

- [ ] **Skills Management**
  - [ ] Skill discovery and installation from UI
  - [ ] Enable/disable skills
  - [ ] Configure installed skills

- [ ] **General Configuration Management**
  - [ ] Configuration editor with validation
  - [ ] Import/export configuration
  - [ ] Backup and restore configuration
  - [ ] Configuration change history

### 2.3 UX/UI Improvements
- [ ] System status dashboard
- [ ] Real-time logs
- [ ] Usage metrics (requests, tokens, active channels)
- [ ] Responsive design
- [ ] Dark/light theme
- [ ] Basic accessibility

### 2.4 Phase 2 Success Criteria
- [ ] All configuration can be done from WebUI
- [ ] No need to manually edit files for common cases
- [ ] Stable WebUI without crashes
- [ ] Load time < 3s
- [ ] E2E tests for critical configuration flows

---

## 🧪 PHASE 3: Comprehensive E2E Test Suite

**Goal**: Guarantee system quality with automated end-to-end testing.

### 3.1 Testing Infrastructure
- [ ] Set up E2E testing framework (Playwright, Cypress, or similar)
- [ ] CI/CD pipeline for E2E tests
- [ ] Isolated testing environment (Docker compose for tests)
- [ ] Mocks for LLM providers
- [ ] Mocks for communication channels

### 3.2 E2E Tests — Channels
- [ ] Telegram: complete message → response flow
- [ ] Discord: complete message → response flow
- [ ] WhatsApp: complete message → response flow
- [ ] Native channel: complete API → response flow
- [ ] Attachment testing across all channels
- [ ] Multi-message session testing
- [ ] Channel reconnection testing

### 3.3 E2E Tests — WebUI
- [ ] Login and authentication
- [ ] Provider configuration
- [ ] Channel configuration
- [ ] Agent management
- [ ] Skills management
- [ ] Log visualization
- [ ] Import/export configuration

### 3.4 E2E Tests — System
- [ ] System startup with minimal configuration
- [ ] System startup with full configuration
- [ ] Configuration migration between versions
- [ ] Recovery after crash
- [ ] Memory and performance testing
- [ ] Concurrency testing (multiple simultaneous channels)

### 3.5 Phase 3 Success Criteria
- [ ] >90% E2E coverage on critical flows
- [ ] Tests executable in CI/CD
- [ ] Reproducible and stable tests
- [ ] Documentation on how to run tests locally

---

## 🔄 PHASE 4: Release System (Stable/Rolling Release)

**Goal**: Two clear update channels for different types of users.

### 4.1 Branch Structure
- [ ] **`main`** → Stable releases
  - Only merges after passing all E2E tests
  - Versioned releases (semver: v1.0.0, v1.1.0, etc.)
  - Documented changelog
  - Only bugfixes and tested features

- [ ] **`development`** → Rolling release
  - New features merged here first
  - Basic tests required
  - Frequent releases
  - Automatic canary builds

### 4.2 Release Process
- [ ] Automated builds with GoReleaser
- [ ] Differentiated CI/CD pipeline per branch
- [ ] Automatic testing before merge to `main`
- [ ] Promotion process: `development` → testing → `main`
- [ ] Automatic changelog generation
- [ ] Release notes for each version

### 4.3 Update System
- [ ] `lele update` command for CLI updates
- [ ] Support for updating to stable or rolling
- [ ] Automatic backup before updating
- [ ] Rollback in case of failure
- [ ] Notification of available updates
- [ ] Update from WebUI

### 4.4 Phase 4 Success Criteria
- [ ] Two functional branches with clear processes
- [ ] CI/CD configured for both branches
- [ ] Functional update system
- [ ] Release process documentation
- [ ] At least one stable and one rolling release published

---

## 🔗 PHASE 5: MCP Support (Model Context Protocol)

**Goal**: Integrate MCP protocol to allow external tools to connect to Lele.

### 5.1 Research & Design
- [ ] Research MCP protocol (current specification)
- [ ] Architecture design for MCP integration
- [ ] Define priority use cases
- [ ] Compatibility analysis with existing skills system

### 5.2 Implementation
- [ ] Implement MCP client in `pkg/mcp/`
- [ ] Support for external MCP servers
- [ ] Support for running Lele as MCP server
- [ ] Integration with existing tools system
- [ ] MCP server configuration from WebUI
- [ ] Integration testing with popular MCP servers

### 5.3 Use Cases
- [ ] Filesystem integration via MCP
- [ ] Database integration via MCP
- [ ] External APIs integration via MCP
- [ ] Development tools integration via MCP

### 5.4 Phase 5 Success Criteria
- [ ] Functional MCP client
- [ ] At least 3 MCP servers tested and working
- [ ] Complete MCP integration documentation
- [ ] E2E tests for MCP flows
- [ ] Configuration from WebUI

---

## 🧠 PHASE 6: Memory System with RAG

**Goal**: Short-term and long-term memory for the agent using Retrieval Augmented Generation.

### 6.1 Memory Architecture
- [ ] Memory architecture design (short vs long term)
- [ ] Vector database selection (SQLite with embeddings, Chroma, Qdrant, etc.)
- [ ] Memory storage schema design
- [ ] Retention and forgetting policies definition

### 6.2 Short-Term Memory
- [ ] Implement session context buffer
- [ ] Automatic summarization of long conversations
- [ ] Prioritization of relevant information in context
- [ ] Integration with existing session system

### 6.3 Long-Term Memory
- [ ] Implement facts and knowledge storage
- [ ] Embedding system for semantic search
- [ ] Implement RAG pipeline (retrieval → augmentation → generation)
- [ ] Automatic indexing of important information
- [ ] Memory update and expiration system

### 6.4 Agent Integration
- [ ] Integrate RAG into agent decision pipeline
- [ ] Configure when and how to retrieve memories
- [ ] Interface to manage memories from WebUI
- [ ] Memory level configuration (how much to remember)

### 6.5 Phase 6 Success Criteria
- [ ] Functional memory system with short and long term
- [ ] RAG integrated into the agent
- [ ] Measurable improvement in contextual responses
- [ ] Memory management from WebUI
- [ ] Retrieval quality tests

---

## 🖥️ PHASE 7: Lele Desktop Client

**Goal**: A desktop client similar to Claude Desktop or Codex Desktop for direct interaction with Lele.

### 7.1 Research & Design
- [ ] Analysis of Claude Desktop and Codex Desktop (UX/features)
- [ ] Technology stack selection (Tauri, Electron, Wails, etc.)
- [ ] Client-server architecture design
- [ ] UI/UX design (wireframes, mockups)

### 7.2 Core Features
- [ ] Chat interface with streaming
- [ ] Conversation/history management
- [ ] Support for multiple agents
- [ ] Integration with Lele native channel
- [ ] System tray and notifications
- [ ] Keyboard shortcuts

### 7.3 Advanced Features
- [ ] Integrated code editor
- [ ] File preview
- [ ] Integrated terminal
- [ ] Project/workspace management
- [ ] Git integration
- [ ] External tools support

### 7.4 Platform
- [ ] Builds for Windows, macOS, Linux
- [ ] Auto-update from stable/rolling channels
- [ ] Lele server connection configuration
- [ ] Local mode (embedded Lele) vs remote

### 7.5 Phase 7 Success Criteria
- [ ] Functional desktop client on 3 platforms
- [ ] UX comparable to Claude Desktop
- [ ] Working auto-update
- [ ] Installation and usage documentation
- [ ] E2E tests for main flows

---

## 🎯 Global Success Metrics

- [ ] 0 crashes in production (stable)
- [ ] < 2s average response time
- [ ] > 90% E2E test coverage
- [ ] 100% configuration from WebUI
- [ ] 2 functional update channels
- [ ] MCP integrated with 3+ servers
- [ ] Functional RAG memory with measurable improvement
- [ ] Desktop client on 3 platforms

---

## 🤝 Contributions

Contributions are welcome for any item on this roadmap! Comment on the relevant issue or submit a PR.

---

> **Note**: This roadmap is a living guide. It will be updated regularly based on progress and priority changes.
