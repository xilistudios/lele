import { describe, expect, mock, test } from 'bun:test'
import { fireEvent, render, screen } from '@testing-library/react'
import '../test/i18n'
import type { Agent, AuthSession, ChatMessage, ChatSession } from '../lib/types'
import { ChatView } from './ChatView'

const auth: AuthSession = {
  token: 'token',
  refresh_token: 'refresh',
  expires: '2026-01-01T00:00:00Z',
  client_id: 'client-1',
  device_name: 'Desktop',
}

const agents: Agent[] = [
  { id: 'main', name: 'Main Agent', workspace: '~/.lele', model: 'gpt-4', default: true },
  { id: 'research', name: 'Research Agent', workspace: '~/.lele/research', model: 'gpt-4.1' },
]

const sessions: ChatSession[] = [
  {
    key: 'native:client-1',
    created: '2026-01-01T00:00:00Z',
    updated: '2026-01-01T00:00:00Z',
    message_count: 1,
  },
]

const messages: ChatMessage[] = [
  {
    id: '1',
    role: 'assistant',
    content: 'Hola',
    streaming: false,
    createdAt: new Date().toISOString(),
  },
]

const diagnostics = {
  status: null,
  channels: [],
  tools: [],
  config: null,
  agentInfo: null,
}

const modelState = {
  current: 'gpt-4',
  available: ['gpt-4', 'gpt-4o-mini'],
  groups: [],
}

describe('ChatView', () => {
  test('renders the current chat state', () => {
    render(
      <ChatView
        apiUrl="http://localhost"
        approvalRequest={null}
        auth={auth}
        agents={agents}
        currentAgentId="main"
        currentSessionKey="native:client-1"
        diagnostics={diagnostics}
        diagnosticsOpen={false}
        error={null}
        modelState={modelState}
        messages={messages}
        onApprove={mock(() => undefined)}
        onAttachmentsChange={mock(() => undefined)}
        onCancel={mock(() => undefined)}
        onClearSession={mock(() => undefined)}
        onCreateSession={mock(() => undefined)}
        onLogout={mock(() => undefined)}
        onSelectAgent={mock(() => undefined)}
        onSelectModel={mock(() => undefined)}
        onSelectSession={mock(() => undefined)}
        onDeleteSession={mock(() => undefined)}
        onSend={mock(() => undefined)}
        onToggleDiagnostics={mock(() => undefined)}
        pendingAttachments={[]}
        sessions={sessions}
        toolStatus={null}
        wsStatus="connected"
      />,
    )

    expect(screen.getByText('Hola')).not.toBeNull()
    expect(screen.getByText('Main Agent')).not.toBeNull()
    expect(screen.getByText(/1 .*mensaje|1 .*message/i)).not.toBeNull()
  })

  test('sends a draft message', () => {
    const onSend = mock(() => undefined)
    render(
      <ChatView
        apiUrl="http://localhost"
        approvalRequest={null}
        auth={auth}
        agents={agents}
        currentAgentId="main"
        currentSessionKey="native:client-1"
        diagnostics={diagnostics}
        diagnosticsOpen={false}
        error={null}
        modelState={modelState}
        messages={[]}
        onApprove={mock(() => undefined)}
        onAttachmentsChange={mock(() => undefined)}
        onCancel={mock(() => undefined)}
        onClearSession={mock(() => undefined)}
        onCreateSession={mock(() => undefined)}
        onLogout={mock(() => undefined)}
        onSelectAgent={mock(() => undefined)}
        onSelectModel={mock(() => undefined)}
        onSelectSession={mock(() => undefined)}
        onDeleteSession={mock(() => undefined)}
        onSend={onSend}
        onToggleDiagnostics={mock(() => undefined)}
        pendingAttachments={[]}
        sessions={sessions}
        toolStatus={null}
        wsStatus="connected"
      />,
    )

    fireEvent.change(screen.getByPlaceholderText('Mensaje...'), {
      target: { value: 'Hola' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Enviar' }))

    expect(onSend).toHaveBeenCalledWith('Hola', [])
  })

  test('shows cancel button when streaming', () => {
    render(
      <ChatView
        apiUrl="http://localhost"
        approvalRequest={null}
        auth={auth}
        agents={agents}
        currentAgentId="main"
        currentSessionKey="native:client-1"
        diagnostics={diagnostics}
        diagnosticsOpen={false}
        error={null}
        modelState={modelState}
        messages={[
          {
            id: 'streaming',
            role: 'assistant',
            content: 'Hol',
            streaming: true,
            createdAt: new Date().toISOString(),
          },
        ]}
        onApprove={mock(() => undefined)}
        onAttachmentsChange={mock(() => undefined)}
        onCancel={mock(() => undefined)}
        onClearSession={mock(() => undefined)}
        onCreateSession={mock(() => undefined)}
        onLogout={mock(() => undefined)}
        onSelectAgent={mock(() => undefined)}
        onSelectModel={mock(() => undefined)}
        onSelectSession={mock(() => undefined)}
        onDeleteSession={mock(() => undefined)}
        onSend={mock(() => undefined)}
        onToggleDiagnostics={mock(() => undefined)}
        pendingAttachments={[]}
        sessions={sessions}
        toolStatus={null}
        wsStatus="connected"
      />,
    )

    expect(screen.getByRole('button', { name: 'Cancelar' })).not.toBeNull()
  })

  test('changes agent from the chat composer selector', () => {
    const onSelectAgent = mock(() => undefined)

    render(
      <ChatView
        apiUrl="http://localhost"
        approvalRequest={null}
        auth={auth}
        agents={agents}
        currentAgentId="main"
        currentSessionKey="native:client-1"
        diagnostics={diagnostics}
        diagnosticsOpen={false}
        error={null}
        modelState={modelState}
        messages={[]}
        onApprove={mock(() => undefined)}
        onAttachmentsChange={mock(() => undefined)}
        onCancel={mock(() => undefined)}
        onClearSession={mock(() => undefined)}
        onCreateSession={mock(() => undefined)}
        onLogout={mock(() => undefined)}
        onSelectAgent={onSelectAgent}
        onSelectModel={mock(() => undefined)}
        onSelectSession={mock(() => undefined)}
        onDeleteSession={mock(() => undefined)}
        onSend={mock(() => undefined)}
        onToggleDiagnostics={mock(() => undefined)}
        pendingAttachments={[]}
        sessions={sessions}
        toolStatus={null}
        wsStatus="connected"
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Agente' }))
    fireEvent.change(screen.getByLabelText('Agente buscar'), {
      target: { value: 'research' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Research Agent' }))

    expect(onSelectAgent).toHaveBeenCalledWith('research')
  })

  test('changes model from the chat composer selector', () => {
    const onSelectModel = mock(() => undefined)

    render(
      <ChatView
        apiUrl="http://localhost"
        approvalRequest={null}
        auth={auth}
        agents={agents}
        currentAgentId="main"
        currentSessionKey="native:client-1"
        diagnostics={diagnostics}
        diagnosticsOpen={false}
        error={null}
        modelState={modelState}
        messages={[]}
        onApprove={mock(() => undefined)}
        onAttachmentsChange={mock(() => undefined)}
        onCancel={mock(() => undefined)}
        onClearSession={mock(() => undefined)}
        onCreateSession={mock(() => undefined)}
        onLogout={mock(() => undefined)}
        onSelectAgent={mock(() => undefined)}
        onSelectModel={onSelectModel}
        onSelectSession={mock(() => undefined)}
        onDeleteSession={mock(() => undefined)}
        onSend={mock(() => undefined)}
        onToggleDiagnostics={mock(() => undefined)}
        pendingAttachments={[]}
        sessions={sessions}
        toolStatus={null}
        wsStatus="connected"
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Modelo' }))
    fireEvent.change(screen.getByLabelText('Modelo buscar'), {
      target: { value: 'gpt-4o-mini' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'gpt-4o-mini' }))

    expect(onSelectModel).toHaveBeenCalledWith('gpt-4o-mini')
  })

  test('locks agent selection after conversation starts', () => {
    render(
      <ChatView
        apiUrl="http://localhost"
        approvalRequest={null}
        auth={auth}
        agents={agents}
        currentAgentId="main"
        currentSessionKey="native:client-1"
        diagnostics={diagnostics}
        diagnosticsOpen={false}
        error={null}
        modelState={modelState}
        messages={messages}
        onApprove={mock(() => undefined)}
        onAttachmentsChange={mock(() => undefined)}
        onCancel={mock(() => undefined)}
        onClearSession={mock(() => undefined)}
        onCreateSession={mock(() => undefined)}
        onLogout={mock(() => undefined)}
        onSelectAgent={mock(() => undefined)}
        onSelectModel={mock(() => undefined)}
        onSelectSession={mock(() => undefined)}
        onDeleteSession={mock(() => undefined)}
        onSend={mock(() => undefined)}
        onToggleDiagnostics={mock(() => undefined)}
        pendingAttachments={[]}
        sessions={sessions}
        toolStatus={null}
        wsStatus="connected"
      />,
    )

    expect((screen.getByLabelText('Agente') as HTMLSelectElement).disabled).toBe(true)
  })
})
