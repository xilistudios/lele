import {
  type ChangeEvent,
  type FormEvent,
  type KeyboardEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useChatPageContext } from '../../contexts/ChatPageContext'
import { useSlashCommands } from '../../hooks/useSlashCommands'
import { getModeTheme } from '../../lib/modeTheme'
import type { SlashCommandInfo } from '../../lib/types'
import { IconButton } from '../atoms/IconButton'
import { CloseIcon, FolderIcon, PlusIcon } from '../atoms/Icons'
import { FolderPickerModal } from '../organisms/FolderPickerModal'
import { AttachmentInput } from './AttachmentInput'
import { QueuedMessages } from './QueuedMessages'
import { SearchableSelect } from './SearchableSelect'
import { SlashCommandMenu } from './SlashCommandMenu'
import { completeDraft, filterCommands, isPaletteTrigger } from './commandPalette'

const IMAGE_EXTENSIONS = new Set(['.png', '.jpg', '.jpeg', '.gif', '.webp', '.bmp', '.svg'])

function isImageByExtension(name: string): boolean {
  const ext = name.toLowerCase().split('.').pop()
  return ext ? IMAGE_EXTENSIONS.has(`.${ext}`) : false
}

function buildFileUrl(apiUrl: string, path: string): string {
  const base = apiUrl.replace(/\/$/, '')
  return `${base}/api/v1/files/view?path=${encodeURIComponent(path)}`
}

export function ChatComposer() {
  const { t } = useTranslation()
  const { canCancel, hasConversation, availableModels, groupedModels, selectedModel, thinkLevel } =
    useChatPageContext()
  const {
    currentAgent,
    agents,
    pendingAttachments,
    onSend,
    onCancel,
    onUploadAttachments,
    onAttachmentsChange,
    onSelectAgent,
    onSelectModel,
    onSelectThinkLevel,
    sessionFolder,
    onSelectFolder,
    onClearFolder,
    chatMode,
    sendTyping,
    currentSessionKey,
  } = useAppLogicContext()
  const { apiUrl, api } = useAuthContext()

  const [draft, setDraft] = useState('')
  const [folderPickerOpen, setFolderPickerOpen] = useState(false)
  // Set when a submit was refused because this session's queue is full; cleared
  // by the next accepted submit (or by editing the draft).
  const [queueFullHint, setQueueFullHint] = useState(false)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const lastTypingSentRef = useRef(0)

  // "/" slash-command palette. The palette only assists composing: accepting a
  // row inserts "<name> " into the draft and the command itself runs on the
  // backend once the text is sent as a normal message (handleCommand there).
  const { commands } = useSlashCommands(api)
  const [paletteIdx, setPaletteIdx] = useState(0)
  // Escape hides the palette until the next edit of the trigger text (the
  // textarea change handler re-arms it).
  const [paletteDismissed, setPaletteDismissed] = useState(false)

  const paletteTriggered = isPaletteTrigger(draft)
  const paletteItems = useMemo(() => filterCommands(commands, draft), [commands, draft])
  const paletteOpen = paletteTriggered && !paletteDismissed && paletteItems.length > 0
  // Items can shrink asynchronously (commands load once, late), so clamp the
  // highlight instead of trusting paletteIdx to stay in range.
  const activePaletteIdx = Math.min(paletteIdx, Math.max(paletteItems.length - 1, 0))

  const selectCommand = (command: SlashCommandInfo) => {
    setDraft(completeDraft(draft, command))
    setPaletteIdx(0)
    setPaletteDismissed(false)
    textareaRef.current?.focus()
  }

  // The composer is not remounted on session switch, so palette state is
  // cleared explicitly. Draft behaviour on switch is left untouched.
  // biome-ignore lint/correctness/useExhaustiveDependencies: currentSessionKey is the trigger, not a read — the effect must run exactly when the session changes.
  useEffect(() => {
    setPaletteIdx(0)
    setPaletteDismissed(false)
  }, [currentSessionKey])

  const submit = (e?: FormEvent) => {
    e?.preventDefault()
    const content = draft.trim()
    if (!content && pendingAttachments.length === 0) return

    // While the agent is busy onSend enqueues instead of sending, and returns
    // false when that session's queue is full — keep the draft so nothing is
    // silently lost.
    const accepted = onSend(content, pendingAttachments)
    if (accepted === false) {
      setQueueFullHint(true)
      return
    }
    setQueueFullHint(false)
    setDraft('')
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto'
    }
  }

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // IME safety: while composing (CJK candidates), every key belongs to the
    // input method — the palette must not swallow them.
    if (e.nativeEvent.isComposing) return

    if (paletteOpen) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setPaletteIdx((idx) => (idx + 1) % paletteItems.length)
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setPaletteIdx((idx) => (idx - 1 + paletteItems.length) % paletteItems.length)
        return
      }
      if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey)) {
        e.preventDefault()
        const command = paletteItems[activePaletteIdx]
        if (command) selectCommand(command)
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        setPaletteDismissed(true)
        return
      }
    }

    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      submit()
    }
  }

  const handleTextareaChange = (e: ChangeEvent<HTMLTextAreaElement>) => {
    setDraft(e.target.value)
    // Any edit restarts the highlight, and typing a new trigger re-arms the
    // palette after an Escape dismissal.
    setPaletteIdx(0)
    if (isPaletteTrigger(e.target.value)) setPaletteDismissed(false)
    if (queueFullHint && e.target.value.trim()) setQueueFullHint(false)
    e.target.style.height = 'auto'
    e.target.style.height = `${Math.min(e.target.scrollHeight, 200)}px`

    const now = Date.now()
    if (now - lastTypingSentRef.current > 2000 && currentSessionKey) {
      lastTypingSentRef.current = now
      sendTyping(currentSessionKey)
    }
  }

  const agentsOptions = agents.map((agent) => ({
    value: agent.id,
    label: agent.name,
  }))
  const selectedAgentId = currentAgent?.id ?? ''

  const composerTheme = getModeTheme(chatMode)

  // Check if the currently selected model has reasoning enabled
  const thinkingEnabled = useMemo(() => {
    if (!selectedModel) return false

    // Normalize model names for comparison (with/without provider prefix)
    const normalizeModelName = (modelName: string): string => {
      const parts = modelName.split('/')
      return parts.length > 1 ? parts[parts.length - 1] : modelName
    }
    const normalizedSelectedModel = normalizeModelName(selectedModel)

    // Build a lookup set of models with reasoning enabled.
    // Only store models that actually have reasoning — flat availableModels
    // lack reasoning metadata, so they must not overwrite grouped model data.
    const modelsWithReasoning = new Set<string>()

    for (const group of groupedModels ?? []) {
      for (const model of group.options) {
        if (model.reasoning?.enable) {
          modelsWithReasoning.add(model.value)
          modelsWithReasoning.add(normalizeModelName(model.value))
        }
      }
    }
    for (const model of availableModels ?? []) {
      if (model.reasoning?.enable) {
        modelsWithReasoning.add(model.value)
        modelsWithReasoning.add(normalizeModelName(model.value))
      }
    }

    return (
      modelsWithReasoning.has(selectedModel) || modelsWithReasoning.has(normalizedSelectedModel)
    )
  }, [selectedModel, groupedModels, availableModels])
  const thinkOptions = [
    { value: 'default', label: t('chat.thinkingDefault') },
    { value: 'off', label: t('chat.thinkingOff') },
    { value: 'low', label: t('chat.thinkingLow') },
    { value: 'medium', label: t('chat.thinkingMedium') },
    { value: 'high', label: t('chat.thinkingHigh') },
  ]
  return (
    <form onSubmit={submit}>
      {/* Messages typed while the agent was busy. Rendered inside the form but
          above the attachments strip; QueuedMessages hides itself when empty. */}
      <QueuedMessages />
      {queueFullHint && (
        <p className="mb-2 px-1 text-xs text-state-error" role="alert">
          {t('chat.queueFull')}
        </p>
      )}
      {pendingAttachments.length > 0 && (
        <div className="mb-3 flex flex-wrap items-end gap-2">
          {pendingAttachments.map((attachment) => {
            const isImg = isImageByExtension(attachment)
            if (isImg) {
              const url = buildFileUrl(apiUrl, attachment)
              return (
                <div
                  key={attachment}
                  className="relative group h-16 w-16 overflow-hidden rounded-lg border border-border bg-background-secondary transition-all hover:border-border-light shadow-sm"
                >
                  <img
                    src={url}
                    alt={attachment.split('/').pop() ?? 'attachment'}
                    className="h-full w-full object-cover"
                  />
                  <div className="absolute inset-0 bg-black/20 opacity-0 group-hover:opacity-100 transition-opacity" />
                  <button
                    type="button"
                    onClick={() =>
                      onAttachmentsChange(pendingAttachments.filter((a) => a !== attachment))
                    }
                    className="absolute right-1 top-1 flex h-4 w-4 items-center justify-center rounded-full bg-black/60 text-white hover:bg-black/80 transition-colors"
                    title={t('chat.removeAttachment') || 'Remove attachment'}
                  >
                    <CloseIcon size={10} />
                  </button>
                </div>
              )
            }

            const filename = attachment.split('/').pop() ?? attachment
            const ext = filename.split('.').pop()?.toUpperCase() ?? 'FILE'
            return (
              <div
                key={attachment}
                className="relative group flex items-center h-16 max-w-[240px] min-w-[160px] gap-2 rounded-lg border border-border bg-background-secondary p-2 transition-all hover:border-border-light shadow-sm pr-8"
              >
                <div className="h-12 w-10 bg-[color-mix(in_srgb,var(--color-accent-primary)_10%,transparent)] text-accent-primary rounded-md flex flex-col items-center justify-center border border-[color-mix(in_srgb,var(--color-accent-primary)_15%,transparent)] flex-shrink-0 select-none">
                  <svg
                    className="h-4 w-4 text-accent-primary mb-0.5"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                    strokeWidth="2.5"
                    aria-hidden="true"
                  >
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z"
                    />
                  </svg>
                  <span className="text-[8px] font-bold tracking-wider leading-none">
                    {ext.slice(0, 4)}
                  </span>
                </div>
                <div className="flex flex-col min-w-0">
                  <span className="text-xs font-medium text-text-primary truncate">{filename}</span>
                  <span className="text-[10px] text-text-tertiary leading-none mt-1">
                    {ext} File
                  </span>
                </div>
                <button
                  type="button"
                  onClick={() =>
                    onAttachmentsChange(pendingAttachments.filter((a) => a !== attachment))
                  }
                  className="absolute right-1.5 top-1/2 -translate-y-1/2 flex h-5 w-5 items-center justify-center rounded-full text-text-tertiary hover:bg-background-tertiary hover:text-text-primary transition-colors"
                  title={t('chat.removeAttachment') || 'Remove attachment'}
                >
                  <CloseIcon size={10} />
                </button>
              </div>
            )
          })}
        </div>
      )}
      <div className="rounded-lg border border-border bg-background-secondary transition-all duration-150 focus-within:border-border-light focus-within:ring-1 focus-within:ring-[color-mix(in_srgb,var(--color-accent-primary)_40%,transparent)]">
        <div className={`h-0.5 w-full rounded-t-lg ${composerTheme.accentBar}`} />
        {/* Top toolbar: folder picker above the input, folder chip when set */}
        <div className="flex items-center gap-1 px-2 pt-1.5">
          <IconButton
            onClick={() => setFolderPickerOpen(true)}
            title={t('chat.addFolder')}
            ariaLabel={t('chat.addFolder')}
          >
            <PlusIcon size={14} />
          </IconButton>
          {sessionFolder && (
            <span
              title={sessionFolder}
              className="flex max-w-[180px] items-center gap-1 rounded-full border border-[color-mix(in_srgb,var(--color-accent-primary)_30%,transparent)] bg-[color-mix(in_srgb,var(--color-accent-primary)_10%,transparent)] px-2 py-0.5 text-accent-primary"
            >
              <FolderIcon size={11} className="flex-shrink-0" />
              <span className="truncate text-[10px] font-medium">
                {sessionFolder.split('/').filter(Boolean).pop() || sessionFolder}
              </span>
              <button
                type="button"
                onClick={onClearFolder}
                title={t('chat.removeFolder')}
                aria-label={t('chat.removeFolder')}
                className="flex h-3.5 w-3.5 flex-shrink-0 items-center justify-center rounded-full hover:bg-[color-mix(in_srgb,var(--color-accent-primary)_25%,transparent)] transition-colors"
              >
                <CloseIcon size={8} />
              </button>
            </span>
          )}
        </div>
        {paletteOpen && (
          <SlashCommandMenu
            items={paletteItems}
            activeIndex={activePaletteIdx}
            onSelect={selectCommand}
            onHover={setPaletteIdx}
          />
        )}
        <textarea
          ref={textareaRef}
          className="min-h-[44px] max-h-[200px] w-full resize-none bg-transparent px-4 pb-2 pt-1.5 text-sm text-text-primary outline-none placeholder:text-text-tertiary"
          placeholder={t('chat.messagePlaceholder')}
          value={draft}
          onChange={handleTextareaChange}
          onKeyDown={handleKeyDown}
          aria-autocomplete="list"
          aria-controls={paletteOpen ? 'slash-command-menu' : undefined}
          aria-activedescendant={paletteOpen ? `slash-command-${activePaletteIdx}` : undefined}
          disabled={false}
          rows={1}
        />
        <div className="flex flex-wrap items-center gap-2 px-3 pb-2 pt-1 sm:gap-3">
          <AttachmentInput
            onUpload={onUploadAttachments}
            onAttach={(paths) => onAttachmentsChange((prev) => [...prev, ...paths])}
          />
          <SearchableSelect
            ariaLabel={t('chat.model')}
            buttonLabel={t('chat.model')}
            emptyLabel={t('chat.default')}
            groups={groupedModels}
            onChange={onSelectModel}
            options={groupedModels ? undefined : availableModels}
            placeholder={selectedModel}
            searchAriaLabel={`${t('chat.model')} buscar`}
            searchPlaceholder={t('chat.model')}
            value={selectedModel}
          />
          {thinkingEnabled && (
            <SearchableSelect
              ariaLabel={t('chat.thinking')}
              buttonLabel={t('chat.thinking')}
              direction="up"
              emptyLabel={t('chat.thinkingOff')}
              onChange={onSelectThinkLevel}
              options={thinkOptions}
              placeholder={
                thinkOptions.find((o) => o.value === thinkLevel)?.label ?? t('chat.thinkingOff')
              }
              searchAriaLabel={`${t('chat.thinking')} buscar`}
              searchPlaceholder={t('chat.thinking')}
              value={thinkLevel}
            />
          )}
          {agentsOptions.length > 1 && (
            <SearchableSelect
              ariaLabel={t('chat.agent')}
              buttonLabel={t('chat.agent')}
              disabled={hasConversation}
              emptyLabel={t('chat.agentLocked')}
              onChange={onSelectAgent}
              options={agentsOptions}
              placeholder={
                agentsOptions.find((a) => a.value === selectedAgentId)?.label ?? t('chat.agent')
              }
              searchAriaLabel={`${t('chat.agent')} buscar`}
              searchPlaceholder={t('chat.agent')}
              value={selectedAgentId}
            />
          )}
          <button
            type={canCancel ? 'button' : 'submit'}
            disabled={false}
            aria-label={canCancel ? t('chat.cancel') : t('chat.send')}
            className={`ml-auto flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md transition-colors ${
              canCancel
                ? 'bg-state-error-light text-state-error hover:bg-state-error hover:text-text-on-accent border border-state-error/30'
                : 'bg-accent-primary text-text-on-accent hover:bg-accent-hover'
            }`}
            onClick={canCancel ? onCancel : undefined}
          >
            {canCancel ? (
              <svg
                width="12"
                height="12"
                viewBox="0 0 24 24"
                fill="currentColor"
                aria-hidden="true"
              >
                <rect x="6" y="6" width="12" height="12" rx="2" />
              </svg>
            ) : (
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
            )}
          </button>
        </div>
      </div>
      <FolderPickerModal
        open={folderPickerOpen}
        onClose={() => setFolderPickerOpen(false)}
        onSelect={(path) => {
          onSelectFolder(path)
          setFolderPickerOpen(false)
        }}
        currentFolder={sessionFolder || undefined}
      />
    </form>
  )
}
