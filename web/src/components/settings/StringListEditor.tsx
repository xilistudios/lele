import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { SearchableSelect } from '../molecules/SearchableSelect'

type Option = {
  value: string
  label: string
}

type OptionGroup = {
  label: string
  options: Option[]
}

type Props = {
  id: string
  value: string[] | null | undefined
  onChange: (value: string[]) => void
  disabled?: boolean
  placeholder?: string
  options?: Option[]
  groups?: OptionGroup[]
  emptyLabel?: string
  loading?: boolean
}

export function StringListEditor({
  id,
  value,
  onChange,
  disabled,
  placeholder,
  options,
  groups,
  emptyLabel,
  loading,
}: Props) {
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

  const handleSelect = (selectedValue: string) => {
    if (selectedValue && !items.includes(selectedValue)) {
      onChange([...items, selectedValue])
    }
    setNewItem('')
  }

  const hasDropdown = options !== undefined || groups !== undefined

  return (
    <div className="space-y-2">
      <div className="flex gap-2">
        {hasDropdown ? (
          <div className="flex-1">
            <SearchableSelect
              ariaLabel={id}
              buttonLabel={placeholder || t('common.add')}
              direction="down"
              disabled={disabled}
              emptyLabel={loading ? t('settings.loading') : (emptyLabel || t('settings.noModels'))}
              groups={groups}
              onChange={handleSelect}
              options={options}
              placeholder={placeholder || t('settings.selectModel')}
              searchAriaLabel={`${id} buscar`}
              searchPlaceholder={placeholder || t('settings.selectModel')}
              value=""
            />
          </div>
        ) : (
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
        )}
        {!hasDropdown && (
          <button
            type="button"
            onClick={addItem}
            disabled={disabled || !newItem.trim()}
            className="rounded bg-blue-600 px-3 py-2 text-xs text-white transition-colors hover:bg-blue-500 disabled:opacity-50"
          >
            {t('common.add')}
          </button>
        )}
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
                <svg
                  width="14"
                  height="14"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                >
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
