import { type ChangeEvent, type FormEvent, type KeyboardEvent, useRef, useState } from 'react'

type Props = {
  placeholder?: string
  disabled?: boolean
  onSubmit: (content: string) => void
}

export function ChatComposer({
  placeholder = 'Type a message...',
  disabled = false,
  onSubmit,
}: Props) {
  const [draft, setDraft] = useState('')
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const submit = (e?: FormEvent) => {
    e?.preventDefault()
    const content = draft.trim()
    if (!content) return

    onSubmit(content)
    setDraft('')
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto'
    }
  }

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      submit()
    }
  }

  const handleTextareaChange = (e: ChangeEvent<HTMLTextAreaElement>) => {
    setDraft(e.target.value)
    e.target.style.height = 'auto'
    e.target.style.height = `${Math.min(e.target.scrollHeight, 200)}px`
  }

  return (
    <form onSubmit={submit}>
      <div className="rounded-lg border border-[#3a3a3a] bg-[#222] transition-colors focus-within:border-[#555]">
        <textarea
          ref={textareaRef}
          className="min-h-[44px] max-h-[200px] w-full resize-none bg-transparent px-4 pb-2 pt-3 text-sm text-white outline-none placeholder:text-[#444]"
          placeholder={placeholder}
          value={draft}
          onChange={handleTextareaChange}
          onKeyDown={handleKeyDown}
          disabled={disabled}
          rows={1}
        />
        <div className="flex items-center justify-end px-3 pb-2 pt-1">
          <button
            type="submit"
            disabled={!draft.trim() || disabled}
            aria-label="Send message"
            className="flex h-7 w-7 items-center justify-center rounded-md bg-white text-black transition-colors hover:bg-[#ddd] disabled:opacity-20 disabled:cursor-not-allowed"
          >
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2.5"
              aria-hidden="true"
            >
              <line x1="12" y1="19" x2="12" y2="5" />
              <polyline points="5 12 12 5 19 12" />
            </svg>
          </button>
        </div>
      </div>
    </form>
  )
}
