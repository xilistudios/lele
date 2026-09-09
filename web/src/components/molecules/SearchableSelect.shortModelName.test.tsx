import { describe, expect, mock, test } from 'bun:test'
import { render } from '@testing-library/react'
import { SearchableSelect } from './SearchableSelect'

/**
 * The display-name logic behind the trigger button moved from a private
 * `formatDisplayValue()` in this file to `lib/modelName.ts` (spec §3.8) so the
 * agents cards and this control cannot drift. These tests pin the observable
 * behaviour of the component after that extraction.
 */
function renderSelect(props: Partial<Parameters<typeof SearchableSelect>[0]> = {}) {
  return render(
    <SearchableSelect
      ariaLabel="Model"
      buttonLabel="Model"
      emptyLabel="no models"
      onChange={mock()}
      options={[{ value: 'anthropic/claude-sonnet-4', label: 'Claude Sonnet 4' }]}
      placeholder="openai.gpt-4o"
      searchAriaLabel="Search models"
      searchPlaceholder="Search"
      value=""
      {...(props as Record<string, unknown>)}
    />,
  )
}

const triggerText = (container: HTMLElement) =>
  (container.querySelector('button span.flex-1') as HTMLElement).textContent

describe('SearchableSelect short model name (extracted helper)', () => {
  test('shortens the dotted provider prefix of the placeholder', () => {
    expect(triggerText(renderSelect().container)).toBe('gpt-4o')
  })

  test('shortens the slash-separated id of the selected option', () => {
    const { container } = renderSelect({
      options: [
        { value: 'openrouter/anthropic/claude-sonnet-4', label: 'anthropic/claude-sonnet-4' },
      ],
      value: 'openrouter/anthropic/claude-sonnet-4',
    })
    expect(triggerText(container)).toBe('claude-sonnet-4')
  })

  test('keeps dotted names whose prefix is not a known provider', () => {
    const { container } = renderSelect({
      // 'ollama' is not in the provider list: the whole segment is kept.
      options: [{ value: 'ollama.llama3.1:70b', label: 'ollama.llama3.1:70b' }],
      value: 'ollama.llama3.1:70b',
    })
    expect(triggerText(container)).toBe('ollama.llama3.1:70b')
  })
})
