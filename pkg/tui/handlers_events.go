package tui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleTickMsg(msg tickMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	m.tickPending = false
	m.clearSelectionFeedback()
	if m.obVerifying {
		m.animationTick++
		cmds = append(cmds, m.tickCmd())
	}
	if m.modalMode == ModalBackgroundExecs && m.bgExecViewMode && m.bgExecViewID != "" {
		output, status, _, _ := m.agentLoop.GetProvidable().GetBackgroundExecOutput(m.bgExecViewID, 5000)
		m.bgExecViewOutput = output
		m.bgExecViewStatus = status
		if status == "running" {
			cmds = append(cmds, m.tickCmd())
		}
		return m.finishUpdate(msg, cmds)
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

func (m *Model) handleOutboundMsg(msg outboundMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
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
		cmds = append(cmds, m.startOutboundListener())
		return m.finishUpdate(msg, cmds)
	}
	if msg.msg.ChatID == m.currentKey {
		switch msg.msg.Event {
		case "subagent.result":
			if taskID := msg.msg.Metadata["task_id"]; taskID != "" {
				delete(m.subagentProgress, taskID)
			}
			if msg.msg.Metadata["resumed"] == "true" && msg.msg.Metadata["injected"] != "true" {
				m.subagentContinuationPending = true
				m.processing = true
				m.startTime = time.Now()
				m.lastDuration = 0
				cmds = append(cmds, m.tickCmd())
			}
			m.updateViewport()
		case "message.stream":
			if m.pendingUserMessage != "" {
				m.pendingUserMessage = ""
				m.renderedBaseKey = ""
			}
			m.currentToolAction = ""
			if msg.msg.MessageID != "" && msg.msg.MessageID != m.currentAssistantMsgID {
				m.currentAssistantMsgID = msg.msg.MessageID
				m.resetStreamState()
			}
			m.currentStream += msg.msg.Content
			m.applyStreamMetrics(msg.msg.Metadata)
			if metadataBool(msg.msg.Metadata["done"]) {
				m.lastTokPerSec = m.streamTokPerSec
			}
			if cmd := m.throttledUpdateViewport(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		case "message.thinking":
			if msg.msg.MessageID != "" && msg.msg.MessageID != m.currentAssistantMsgID {
				m.currentAssistantMsgID = msg.msg.MessageID
				m.resetStreamState()
			}
			m.currentThinking += msg.msg.Content
			m.applyStreamMetrics(msg.msg.Metadata)
			if cmd := m.throttledUpdateViewport(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		case "command.applied":
			m.currentToolAction = formatCommandApplied(msg.msg.Metadata)
			m.updateViewport()
		case "tool.executing":
			if msg.msg.MessageID != "" && msg.msg.MessageID != m.currentAssistantMsgID {
				m.currentStream = ""
				m.currentThinking = ""
				m.currentAssistantMsgID = msg.msg.MessageID
			}

			toolName := msg.msg.Metadata["tool"]
			action := msg.msg.Metadata["action"]
			if action != "" {
				m.currentToolAction = sanitizeDisplayText(action)
			} else if toolName != "" {
				m.currentToolAction = sanitizeDisplayText(toolName)
			}
			m.updateViewport()
		case "tool.result":
			m.currentToolAction = ""
			m.approvalResult = ""
			if msg.msg.Metadata["tool"] == "spawn" {
				if saKey := msg.msg.Metadata["subagent_session_key"]; saKey != "" {
					if idx := strings.LastIndex(saKey, ":"); idx >= 0 {
						delete(m.subagentProgress, saKey[idx+1:])
					}
				}
			}
			m.updateViewport()
		case "group.status":
			groupID := msg.msg.Metadata["group_id"]
			if groupID == "" {
				break
			}
			status := msg.msg.Metadata["status"]
			if m.groupStatus == nil {
				m.groupStatus = make(map[string]string)
			}
			m.groupStatus[groupID] = status
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
			if status == "done" || status == "stopped" || status == "error" {
				m.processing = false
			}
			m.updateViewport()
		case "group.turn":
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
			m.flushStreamUpdate()
			if m.streamTokPerSec > 0 {
				m.lastTokPerSec = m.streamTokPerSec
			}
			idMatches := msg.msg.MessageID == m.currentMessageID ||
				msg.msg.ReplyTo == m.currentMessageID ||
				(m.currentMessageID != "" && strings.HasPrefix(msg.msg.MessageID, m.currentMessageID+"-"))

			if m.processing || m.subagentContinuationPending || idMatches {
				cmds = append(cmds, m.completeCmd())
			}
		}
	} else if m.isSubagentOfCurrentChat(msg.msg.ChatID) {
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
				m.recordSubagentProgress(taskID, "finalizing…")
				m.updateViewport()
			}
		}
	}
	cmds = append(cmds, m.startOutboundListener())

	return m.finishUpdate(msg, cmds)
}
