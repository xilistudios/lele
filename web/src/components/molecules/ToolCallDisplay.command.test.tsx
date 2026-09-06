import { describe, expect, mock, test } from 'bun:test'
import { render } from '@testing-library/react'
import i18n from '../../test/i18n'
import { ToolCallDisplay, parseCommandArgs } from './ToolCallDisplay'

// The shared test i18n instance boots in Spanish; read the label through the
// same key the component uses so the assertion holds for any active locale.
const LABEL = i18n.t('toolCalls.commandApplied')

// toolArgs as written by hooks/event-handlers/command.ts: "command {json}".
function commandArgs(overrides: Record<string, string> = {}): string {
  return `command ${JSON.stringify({
    command: '/review',
    args: 'src/main.go',
    agent: 'coder',
    model: 'openai/gpt-5',
    source: 'workspace',
    description: 'Review the current diff',
    ...overrides,
  })}`
}

describe('parseCommandArgs', () => {
  test('parses the command payload', () => {
    expect(parseCommandArgs(commandArgs())).toEqual({
      command: '/review',
      args: 'src/main.go',
      agent: 'coder',
      model: 'openai/gpt-5',
      source: 'workspace',
      description: 'Review the current diff',
    })
  })

  test('returns null for missing, non-JSON or slash-less payloads', () => {
    expect(parseCommandArgs(undefined)).toBeNull()
    expect(parseCommandArgs('exec ls')).toBeNull()
    expect(parseCommandArgs('command {"args":"x"}')).toBeNull()
  })
})

describe('ToolCallDisplay for tool "command"', () => {
  const props = {
    toolName: 'command',
    toolStatus: 'completed' as const,
    expanded: false,
    onToggleExpand: mock(() => {}),
  }

  test('renders label, "/name args" and agent/model/source badges', () => {
    const { getByTestId } = render(<ToolCallDisplay {...props} toolArgs={commandArgs()} />)

    const card = getByTestId('command-applied-card')
    expect(card.textContent).toContain(LABEL)
    expect(card.textContent).toContain('/review src/main.go')
    expect(card.textContent).toContain('coder')
    expect(card.textContent).toContain('openai/gpt-5')
    expect(card.textContent).toContain('workspace')
  })

  test('omits empty badges and shows the bare command when there are no args', () => {
    const { getByTestId } = render(
      <ToolCallDisplay
        {...props}
        toolArgs={commandArgs({ args: '', agent: '', model: '', source: '' })}
      />,
    )

    const card = getByTestId('command-applied-card')
    expect(card.textContent).toContain('/review')
    expect(card.textContent).not.toContain('openai/gpt-5')
    expect(card.textContent).not.toContain('workspace')
  })

  test('falls back to the generic card when the payload is malformed', () => {
    const { queryByTestId, container } = render(
      <ToolCallDisplay {...props} toolArgs="command not-json" />,
    )

    expect(queryByTestId('command-applied-card')).toBeNull()
    expect(container.textContent).toContain(LABEL)
  })
})
