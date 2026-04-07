import { useState } from 'react'
import { useTranslation } from 'react-i18next'

type Props = {
  id: string
  value: string[] | null | undefined
  onChange: (value: string[]) => void
  disabled?: boolean
  placeholder?: string
}

export function StringListEditor({ id, value, onChange, disabled, placeholder }: Props) {
  const { t } = useTranslation()
  const removeTitle = t('common.remove')
  const [newItem, setNewItem] = useState('')
  const items = value ?? []

  const addItem = () => {
    if (newItem.trim()) {
      onChange([...items, newItem.trim()])
      setNewItem('')
    }
  }

  const removeItem = (index: number) => {
    onChange(items.filter((_, i) => i !== index))
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      addItem()
    }
  }

  return (
    <div className="space-y-2">
      <div className="flex gap-2">
        <input
          id={id}
          type="text"
          value={newItem}
          onChange={(e) => setNewItem(e.target.value)}
          onKeyDown={handleKeyDown}
          disabled={disabled}
          placeholder={placeholder}
          className="flex-1 rounded border border-[#3a3a3a] bg-[#1a1a1a] px-3 py-2 text-xs text-[#e0e0e0] placeholder-[#555] focus:border-blue-500 focus:outline-none disabled:opacity-50"
        />
        <button
          type="button"
          onClick={addItem}
          disabled={disabled || !newItem.trim()}
          className="rounded bg-blue-600 px-3 py-2 text-xs text-white transition-colors hover:bg-blue-500 disabled:opacity-50"
        >
          {t('common.add')}
        </button>
      </div>
      {items.length > 0 && (
        <div className="space-y-1">
          {items.map((item, index) => (
            <div
              key={`${item}-${index}`}
              className="flex items-center justify-between rounded bg-[#2a2a2a] px-3 py-2"
            >
              <span className="text-xs text-[#ccc]">{item}</span>
              <button
                type="button"
                onClick={() => removeItem(index)}
                disabled={disabled}
                title={removeTitle}
                className="text-rose-400 transition-colors hover:text-rose-300 disabled:opacity-50"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <path d="M18 6L6 18M6 6l12 12" />
                </svg>
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
