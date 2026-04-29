import { useQuery } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useSettings } from '../../../contexts/SettingsContext'
import { AddButton } from '../../atoms/AddButton'

const OPENAI_COMPATIBLE_TYPES = new Set([
  'openai',
  'gpt',
  'groq',
  'openrouter',
  'deepseek',
  'ollama',
  'vllm',
  'nvidia',
  'moonshot',
  'nanogpt',
  'chutes',
  'alibaba',
  'alibaba_coding_plan',
  'shengsuanyun',
  'zai_coding_plan',
  'zai',
  'modelark_coding_plan',
  'modelark',
  'custom',
])

export function isOpenAICompatible(type: string | undefined): boolean {
  if (!type) return false
  return OPENAI_COMPATIBLE_TYPES.has(type)
}

const INPUT_CLS =
  'w-full rounded border border-border bg-background-primary px-3 py-2 text-xs text-text-primary placeholder:text-text-tertiary focus:border-interaction-primary focus:outline-none focus:ring-2 focus:ring-interaction-primary focus:ring-offset-2 focus:ring-offset-background-primary disabled:opacity-40'

type Props = {
  providerName: string
  providerType: string | undefined
  existingModels: string[]
  onAddModel: (modelName: string) => void
}

export function ModelSearchInput({
  providerName,
  providerType,
  existingModels,
  onAddModel,
}: Props) {
  const { t } = useTranslation()
  const { api } = useSettings()
  const [query, setQuery] = useState('')
  const [isOpen, setIsOpen] = useState(false)
  const [selectedIndex, setSelectedIndex] = useState(-1)
  const wrapperRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)

  const enabled = isOpenAICompatible(providerType)

  const {
    data: response,
    isLoading,
    error,
  } = useQuery({
    queryKey: ['providerModels', providerName],
    queryFn: () => api.providerModels(providerName),
    enabled: enabled && isOpen,
    staleTime: 60_000,
    retry: 1,
  })

  const models = response?.models ?? []
  const existingSet = new Set(existingModels)

  const filtered = query.trim()
    ? models.filter((m) => m.id.toLowerCase().includes(query.toLowerCase().trim()))
    : models

  const selectable = filtered.filter((m) => !existingSet.has(m.id))

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (wrapperRef.current && !wrapperRef.current.contains(e.target as Node)) {
        setIsOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  const prevSelectableLenRef = useRef(selectable.length)
  if (selectable.length !== prevSelectableLenRef.current) {
    prevSelectableLenRef.current = selectable.length
    if (selectedIndex >= selectable.length) {
      setSelectedIndex(-1)
    }
  }

  useEffect(() => {
    if (selectedIndex >= 0 && listRef.current) {
      const items = listRef.current.children
      if (items[selectedIndex]) {
        items[selectedIndex].scrollIntoView({ block: 'nearest' })
      }
    }
  }, [selectedIndex])

  const handleSelect = useCallback(
    (modelId: string) => {
      onAddModel(modelId)
      setQuery('')
      setIsOpen(false)
      setSelectedIndex(-1)
      inputRef.current?.focus()
    },
    [onAddModel],
  )

  const handleAddManual = useCallback(() => {
    const key = query.trim()
    if (!key) return
    onAddModel(key)
    setQuery('')
    setIsOpen(false)
  }, [query, onAddModel])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Escape') {
        setIsOpen(false)
        return
      }
      if (!enabled || !isOpen || selectable.length === 0) {
        if (e.key === 'Enter') {
          e.preventDefault()
          handleAddManual()
        }
        return
      }
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setSelectedIndex((i) => Math.min(i + 1, selectable.length - 1))
      } else if (e.key === 'ArrowUp') {
        e.preventDefault()
        setSelectedIndex((i) => Math.max(i - 1, -1))
      } else if (e.key === 'Enter') {
        e.preventDefault()
        if (selectedIndex >= 0 && selectedIndex < selectable.length) {
          handleSelect(selectable[selectedIndex].id)
        } else {
          handleAddManual()
        }
      }
    },
    [enabled, isOpen, selectable, selectedIndex, handleSelect, handleAddManual],
  )

  const showDropdown = enabled && isOpen

  return (
    <div className="flex gap-2" ref={wrapperRef}>
      <div className="relative flex-1">
        <input
          ref={inputRef}
          type="text"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setSelectedIndex(-1)
            if (enabled && !isOpen) setIsOpen(true)
          }}
          onFocus={() => {
            if (enabled) setIsOpen(true)
          }}
          onKeyDown={handleKeyDown}
          placeholder={
            enabled ? t('settings.searchModelsPlaceholder') : t('settings.modelNamePlaceholder')
          }
          className={INPUT_CLS}
        />
        {showDropdown && (
          <div className="absolute z-50 mt-1 max-h-60 w-full overflow-hidden rounded border border-border bg-background-secondary shadow-lg">
            {isLoading && (
              <div className="px-3 py-2 text-xs text-text-tertiary">
                {t('settings.loadingModels')}
              </div>
            )}
            {error && (
              <div className="px-3 py-2 text-xs text-text-tertiary">
                {t('settings.errorLoadingModels')}
              </div>
            )}
            {!isLoading && !error && selectable.length === 0 && query.trim() !== '' && (
              <div className="px-3 py-2 text-xs text-text-tertiary">
                {t('settings.noModelsFound')}
              </div>
            )}
            {!isLoading && !error && (
              <div ref={listRef} className="max-h-52 overflow-y-auto">
                {selectable.map((m, i) => (
                  <button
                    type="button"
                    key={m.id}
                    className={`block w-full cursor-pointer px-3 py-1.5 text-left text-xs font-mono ${
                      i === selectedIndex
                        ? 'bg-interaction-primary/20 text-text-primary'
                        : 'text-text-primary hover:bg-background-tertiary'
                    }`}
                    onClick={() => handleSelect(m.id)}
                    onMouseEnter={() => setSelectedIndex(i)}
                  >
                    {m.id}
                    {m.owned_by && <span className="ml-2 text-text-tertiary">({m.owned_by})</span>}
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
      </div>
      <AddButton onClick={handleAddManual} disabled={!query.trim()}>
        {t('common.add')}
      </AddButton>
    </div>
  )
}
