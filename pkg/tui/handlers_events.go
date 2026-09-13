package tui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// handleTickMsg drives animations: the busy spinner, the onboarding verify
// spinner, background-exec output refresh and the ESC-hint timeout.
func (m *Model) handleTickMsg(msg tickMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	// Reset tick pending flag to allow the next tick to be scheduled
	m.tickPending = false
	// Clear selection feedback after timeout
	m.clearSelectionFeedback()
	// Onboarding verify spinner — animate while we wait for the async
	// key validation result.
	if m.obVerifying {
		m.animationTick++
		cmds = append(cmds, m.tickCmd())
	}
	// Refresh background exec output if viewing a process
	if m.modalMode == ModalBackgroundExecs && m.bgExecViewMode && m.bgExecViewID != "" {
		output, status, _, _ := m.agentLoop.GetProvidable().GetBackgroundExecOutput(m.bgExecViewID, 5000)
		m.bgExecViewOutput = output
		m.bgExecViewStatus = status
		if status == "running" {
			cmds = append(cmds, m.tickCmd())
		}
		// Don't fall through to the session processing tick
		return m.finishUpdate(msg, cmds) // bg-exec view refresh: skip the session-processing tick
	}
	if m.isSessionProcessing() {
		m.elapsedTime = time.Since(m.startTime)
		m.animationTick++
		cmds = append(cmds, m.tickCmd())
	}
	if m.escHint && time.Since(m.escLastPress) > escHintTimeout {
		m.escHint = false
		m.escPressCount = 0
	}

	return m.finishUpdate(msg, cmds)
}

// handleOutboundMsg consumes one event from the agent's outbound channel:
// approval requests, streaming updates, tool progress, subagent results and
// group-mode transcripts. The listener is always re-armed before returning.
// The inner event switch keeps its original `break` semantics: those break
// back to the listener re-arm below, never out of this handler.
func (m *Model) handleOutboundMsg(msg outboundMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	// Approval prompts must never be dropped by the currentKey gate below:
	// the blocked agent waits on ApprovalManager's full timeout otherwise.
	// Requests for the visible session surface immediately; requests for
	// background sessions are stashed and restored on switch.
	if msg.msg.Event == "approval.request" {
		id := msg.msg.Metadata["id"]
		command := msg.msg.Metadata["command"]
		reason := msg.msg.Metadata["reason"]
		if msg.msg.ChatID == m.currentKey {
			m.queueApprovalForCurrentChat(id, command, reason)
		} else {
			m.stashApprovalForSession(msg.msg.ChatID, id, command, reason)
		}
		m.updateViewport()
		// Re-arm the listener exactly like the tail of this case does;
		// a bare break here would leave the TUI deaf to every later event.
		cmds = append(cmds, m.startOutboundListener())
		return m.finishUpdate(msg, cmds) // approval.request re-arms the listener above
	}
	if msg.msg.ChatID == m.currentKey {
		switch msg.msg.Event {
		case "subagent.result":
			// The task is finished: drop its real-time progress line so the
			// overlay does not keep showing stale activity for a completed
			// subagent (TUI-M4). The result itself is queued as a system
			// message for the parent.
			if taskID := msg.msg.Metadata["task_id"]; taskID != "" {
				delete(m.subagentProgress, taskID)
			}
			// Keep loading active while that continuation waits for the session
			// lock, even if the original turn has already completed.
			if !m.processing {
				m.parentCompletionObserved = true
			}
			m.pendingSubagentCompletions++
			m.processing = true
			m.startTime = time.Now()
			m.lastDuration = 0
			m.invalidateSubagentsCache() // subagent status changed
			m.updateViewport()
			cmds = append(cmds, m.tickCmd())
		case "message.stream":
			// Clear pending user message on first stream chunk — by the time
			// the LLM starts streaming, the user message is already in history.
			if m.pendingUserMessage != "" {
				m.pendingUserMessage = ""
				m.renderedBaseKey = "" // invalidate cache so history re-renders without duplicate
			}
			m.currentToolAction = "" // streaming text means tool call is done
			// Only reset streaming state if the MessageID is truly different.
			if msg.msg.MessageID != "" && msg.msg.MessageID != m.currentAssistantMsgID {
				m.currentAssistantMsgID = msg.msg.MessageID
				m.resetStreamState()
			}
			m.currentStream += msg.msg.Content
			if cmd := m.throttledUpdateViewport(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		case "message.thinking":
			// Only reset streaming state if the MessageID is truly different.
			if msg.msg.MessageID != "" && msg.msg.MessageID != m.currentAssistantMsgID {
				m.currentAssistantMsgID = msg.msg.MessageID
				m.resetStreamState()
			}
			m.currentThinking += msg.msg.Content
			if cmd := m.throttledUpdateViewport(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		case "command.applied":
			// A custom (harness) slash command was expanded by the backend
			// for this turn. Render it like a tool activity line: the
			// overlay shows it until the stream resumes (same lifecycle and
			// styling as "tool.executing" below).
			m.currentToolAction = formatCommandApplied(msg.msg.Metadata)
			m.updateViewport()
		case "tool.executing":
			// Only clear streaming state if it's a different message.
			if msg.msg.MessageID != "" && msg.msg.MessageID != m.currentAssistantMsgID {
				m.currentStream = ""
				m.currentThinking = ""
				m.currentAssistantMsgID = msg.msg.MessageID
			}

			// Show the currently executing tool call in the viewport.
			// Use the compact "tool: action" format from metadata.
			toolName := msg.msg.Metadata["tool"]
			action := msg.msg.Metadata["action"]
			if action != "" {
				m.currentToolAction = action
			} else if toolName != "" {
				m.currentToolAction = toolName
			}
			m.updateViewport()
		case "tool.result":
			// Tool completed; clear the active tool action display.
			m.currentToolAction = ""
			// Clear any approval result feedback when the tool completes.
			m.approvalResult = ""
			// When a spawn tool completes, clear its subagent progress entry.
			if msg.msg.Metadata["tool"] == "spawn" {
				m.invalidateSubagentsCache() // a new subagent task now exists
				if saKey := msg.msg.Metadata["subagent_session_key"]; saKey != "" {
					// Extract the task ID suffix (e.g. "subagent-1") from the session key
					if idx := strings.LastIndex(saKey, ":"); idx >= 0 {
						delete(m.subagentProgress, saKey[idx+1:])
					}
				}
			}
			m.updateViewport()
		case "group.status":
			// Group status event: started/done/stopped/error
			groupID := msg.msg.Metadata["group_id"]
			if groupID == "" {
				break
			}
			status := msg.msg.Metadata["status"]
			if m.groupStatus == nil {
				m.groupStatus = make(map[string]string)
			}
			m.groupStatus[groupID] = status
			// Track active group and store participants metadata
			if status == "started" {
				if m.activeGroupID == "" {
					m.activeGroupID = groupID
				}
				m.processing = true
				if participants := msg.msg.Metadata["participants"]; participants != "" {
					if m.groupMeta == nil {
						m.groupMeta = make(map[string]groupMeta)
					}
					if gm, ok := m.groupMeta[groupID]; ok {
						gm.participants = participants
						m.groupMeta[groupID] = gm
					} else {
						m.groupMeta[groupID] = groupMeta{participants: participants}
					}
				}
			}
			// Stop processing when the group finishes
			if status == "done" || status == "stopped" || status == "error" {
				m.processing = false
			}
			m.updateViewport()
		case "group.turn":
			// Group turn: an agent produced a response within the group
			groupID := msg.msg.Metadata["group_id"]
			if groupID == "" {
				break
			}
			if m.groupTranscripts == nil {
				m.groupTranscripts = make(map[string][]groupTurn)
			}
			layer, _ := strconv.Atoi(msg.msg.Metadata["layer"])
			turnIndex, _ := strconv.Atoi(msg.msg.Metadata["turn_index"])
			turn := groupTurn{
				index:   turnIndex,
				layer:   layer,
				speaker: msg.msg.Metadata["speaker"],
				label:   msg.msg.Metadata["label"],
				role:    msg.msg.Metadata["role"],
				content: msg.msg.Content,
			}
			m.groupTranscripts[groupID] = append(m.groupTranscripts[groupID], turn)
			m.processing = true
			if m.activeGroupID == "" {
				m.activeGroupID = groupID
			}
			m.updateViewport()
		case "group.complete":
			// Group complete: final synthesis available
			groupID := msg.msg.Metadata["group_id"]
			if groupID == "" {
				break
			}
			if m.groupMeta == nil {
				m.groupMeta = make(map[string]groupMeta)
			}
			layers, _ := strconv.Atoi(msg.msg.Metadata["layers"])
			totalTokens, _ := strconv.Atoi(msg.msg.Metadata["total_tokens"])
			gm := m.groupMeta[groupID]
			gm.strategy = msg.msg.Metadata["strategy"]
			gm.layers = layers
			gm.totalTokens = totalTokens
			gm.synthesis = msg.msg.Content
			m.groupMeta[groupID] = gm
			m.processing = false
			m.recordGroupCompleteStatus(groupID)
			m.updateViewport()
		case "":
			// Stream finished — flush any pending throttled viewport update
			// so the final content is visible immediately.
			m.flushStreamUpdate()
			idMatches := msg.msg.MessageID == m.currentMessageID ||
				msg.msg.ReplyTo == m.currentMessageID ||
				(m.currentMessageID != "" && strings.HasPrefix(msg.msg.MessageID, m.currentMessageID+"-"))

			if m.processing && (m.pendingSubagentCompletions > 0 || idMatches) {
				cmds = append(cmds, m.completeCmd())
			}
		}
	} else if m.isSubagentOfCurrentChat(msg.msg.ChatID) {
		// Events from subagents spawned by the current parent chat.
		// Capture their tool.executing / tool.result events to show
		// real-time progress in the parent viewport.
		taskID := m.currentSubagentTaskID(msg.msg.ChatID)
		if taskID != "" {
			switch msg.msg.Event {
			case "tool.executing":
				action := msg.msg.Metadata["action"]
				if action == "" {
					action = msg.msg.Metadata["tool"]
				}
				if action != "" {
					m.recordSubagentProgress(taskID, action)
					m.updateViewport()
				}
			case "message.stream":
				// Subagent is streaming a final response — update progress label.
				m.recordSubagentProgress(taskID, "finalizing…")
				m.updateViewport()
			}
		}
	}
	cmds = append(cmds, m.startOutboundListener())

	return m.finishUpdate(msg, cmds)
}
