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
import { endpoints } from '../../services/http/endpoints'
import { IconButton } from '../atoms/IconButton'
import { CloseIcon, FolderIcon, PlusIcon } from '../atoms/Icons'
import { StreamMetricsChip } from '../atoms/StreamMetricsChip'
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

// Pending uploads live in tmp/uploads (public endpoint, no auth needed), so
// they use the public /api/v1/files/view path via the endpoints constant.
function buildFileUrl(apiUrl: string, path: string): string {
  const base = apiUrl.replace(/\/$/, '')
  return `${base}${endpoints.files.view}?path=${encodeURIComponent(path)}`
}

/**
 * Folder chip: the session folder plus its clear button. Rendered twice — in
 * the toolbar row (md+) and at the start of the controls row (mobile, where the
 * toolbar row is hidden) — so the markup lives here instead of being duplicated.
 */
function FolderChip({
  folder,
  onClear,
  className,
}: {
  folder: string
  onClear: () => void
  className?: string
}) {
  const { t } = useTranslation()
  return (
    <span
      title={folder}
      data-testid="folder-chip"
      className={`flex max-w-[180px] items-center gap-1 rounded-full border border-accent-primary/30 bg-accent-primary/10 px-2 py-0.5 text-accent-primary ${className ?? ''}`}
    >
      <FolderIcon size={11} className="flex-shrink-0" />
      <span className="truncate text-2xs font-medium">
        {folder.split('/').filter(Boolean).pop() || folder}
      </span>
      <button
        type="button"
        onClick={onClear}
        title={t('chat.removeFolder')}
        aria-label={t('chat.removeFolder')}
        className="flex h-3.5 w-3.5 flex-shrink-0 items-center justify-center rounded-full hover:bg-accent-primary/25 transition-colors"
      >
        <CloseIcon size={8} />
      </button>
    </span>
  )
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
  // In-flight send counter. A boolean would be reset by the first send's
  // finally-block while a second overlapping send is still pending; a counter
  // keeps the guard armed until ALL concurrent sends complete.
  const inFlightRef = useRef(0)
  const lastSubmittedContentRef = useRef('')

  // "/" slash-command palette. The palette only assists composing: accepting a
  // row inserts "<name> " into the draft and the command itself runs on the
  // backend once the text is sent as a normal message (handleCommand there).
  // Scoped to the agent that will answer, because custom commands live in each
  // agent's own workspace (the built-in half is global and unaffected).
  const { commands } = useSlashCommands(api, currentAgent?.id)
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

  // The composer is not remounted on session switch, so draft/palette/queue
  // state must be cleared explicitly — otherwise text typed in one chat would
  // bleed into the next.
  // biome-ignore lint/correctness/useExhaustiveDependencies: currentSessionKey is the trigger, not a read — the effect must run exactly when the session changes.
  useEffect(() => {
    setDraft('')
    setPaletteIdx(0)
    setPaletteDismissed(false)
    setQueueFullHint(false)
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto'
    }
  }, [currentSessionKey])

  const submit = async (e?: FormEvent) => {
    e?.preventDefault()
    const content = draft.trim()
    if (!content && pendingAttachments.length === 0) return
    // Guard against rapid double-submit of the same content (e.g., double-click).
    // Different content is allowed through for queue operations while the agent
    // is busy — the counter blocks same-content duplicates while ANY send is
    // in flight, content comparison permits distinct messages.
    if (inFlightRef.current > 0 && content === lastSubmittedContentRef.current) return
    inFlightRef.current += 1
    lastSubmittedContentRef.current = content

    try {
      // While the agent is busy onSend enqueues instead of sending, and returns
      // false when that session's queue is full — keep the draft so nothing is
      // silently lost. Same contract applies when session/agent creation fails.
      const accepted = await onSend(content, pendingAttachments)
      if (accepted === false) {
        setQueueFullHint(true)
        return
      }
      setQueueFullHint(false)
      setDraft('')
      if (textareaRef.current) {
        textareaRef.current.style.height = 'auto'
      }
    } finally {
      inFlightRef.current -= 1
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
    const cap = Number.parseFloat(getComputedStyle(e.target).maxHeight)
    e.target.style.height = `${Math.min(e.target.scrollHeight, Number.isFinite(cap) ? cap : 200)}px`

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
        <div className="mb-3 flex flex-wrap items-end gap-2 max-md:max-h-20 max-md:overflow-y-auto">
          {pendingAttachments.map((attachment) => {
            const isImg = isImageByExtension(attachment)
            if (isImg) {
              const url = buildFileUrl(apiUrl, attachment)
              return (
                <div
                  key={attachment}
                  className="relative group h-16 w-16 overflow-hidden rounded-lg border border-border bg-background-secondary transition-all hover:border-border shadow-sm"
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
                    className="absolute right-1 top-1 flex h-4 w-4 items-center justify-center rounded-full bg-overlay text-text-on-accent hover:bg-overlay transition-colors"
                    title={t('chat.removeAttachment')}
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
                className="relative group flex items-center h-16 max-w-[240px] min-w-[160px] gap-2 rounded-lg border border-border bg-background-secondary p-2 transition-all hover:border-border shadow-sm pr-8"
              >
                <div className="h-12 w-10 bg-accent-primary/10 text-accent-primary rounded-md flex flex-col items-center justify-center border border-accent-primary/15 flex-shrink-0 select-none">
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
                  <span className="text-2xs font-bold tracking-wider leading-none">
                    {ext.slice(0, 4)}
                  </span>
                </div>
                <div className="flex flex-col min-w-0">
                  <span className="text-xs font-medium text-text-primary truncate">{filename}</span>
                  <span className="text-2xs text-text-tertiary leading-none mt-1">{ext} File</span>
                </div>
                <button
                  type="button"
                  onClick={() =>
                    onAttachmentsChange(pendingAttachments.filter((a) => a !== attachment))
                  }
                  className="absolute right-1.5 top-1/2 -translate-y-1/2 flex h-5 w-5 items-center justify-center rounded-full text-text-tertiary hover:bg-background-tertiary hover:text-text-primary transition-colors"
                  title={t('chat.removeAttachment')}
                >
                  <CloseIcon size={10} />
                </button>
              </div>
            )
          })}
        </div>
      )}
      <div className="rounded-lg border border-border bg-background-secondary transition-all duration-fast focus-within:border-border focus-within:ring-1 focus-within:ring-accent-primary/40">
        <div className={`h-0.5 w-full rounded-t-lg ${composerTheme.accentBar}`} />
        {/* Top toolbar: folder picker above the input, folder chip when set */}
        <div className="flex items-center gap-1 px-2 pt-1.5 max-md:hidden">
          <IconButton
            onClick={() => setFolderPickerOpen(true)}
            title={t('chat.addFolder')}
            ariaLabel={t('chat.addFolder')}
          >
            <PlusIcon size={14} />
          </IconButton>
          {sessionFolder && <FolderChip folder={sessionFolder} onClear={onClearFolder} />}
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
          className="min-h-[44px] max-h-[200px] max-md:max-h-[min(200px,30dvh)] w-full resize-none bg-transparent px-4 pb-2 pt-1.5 text-sm text-text-primary outline-none placeholder:text-text-tertiary"
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
        {/* The control strip is two-tiered below `md` and one line from `md` up.
            The send button lives OUTSIDE the wrapping group, so no tier split can
            ever push it to a second line. The group is `md:contents`, so at ≥768px
            its box disappears and its children become direct flex items of this
            row — the exact pre-fix geometry, where StreamMetricsChip's ml-auto
            shares the free space with send's ml-auto.
            The tiered layout is a `max-md`-only construct, and `max-md` is the
            ONLY boundary the tiers use: the thinking/agent wrapper is `md:contents`
            too, so it collapses at the same 768px the group does. A `sm:contents`
            wrapper would dissolve the second tier while the `max-md:` utilities
            still apply, leaving 640–767px with five selectors fighting for one
            line (the model trigger collapses to a blank pill there). */}
        <div className="flex flex-wrap items-center gap-2 px-3 pb-2 pt-1 max-md:gap-1.5 sm:gap-3">
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2 max-md:gap-1.5 md:contents">
            <IconButton
              onClick={() => setFolderPickerOpen(true)}
              title={t('chat.addFolder')}
              ariaLabel={t('chat.addFolder')}
              className="md:hidden max-md:flex max-md:h-11 max-md:w-11 max-md:items-center max-md:justify-center"
            >
              <PlusIcon size={14} />
            </IconButton>
            <AttachmentInput
              onUpload={onUploadAttachments}
              onAttach={(paths) => onAttachmentsChange((prev) => [...prev, ...paths])}
              className="max-md:flex max-md:h-11 max-md:w-11 max-md:items-center max-md:justify-center"
            />
            <SearchableSelect
              className="max-md:min-w-0 max-md:flex-1"
              ariaLabel={t('chat.model')}
              buttonLabel={t('chat.model')}
              emptyLabel={t('chat.default')}
              groups={groupedModels}
              onChange={onSelectModel}
              options={groupedModels ? undefined : availableModels}
              placeholder={selectedModel}
              searchAriaLabel={`${t('chat.model')} ${t('chat.search')}`}
              searchPlaceholder={t('chat.model')}
              value={selectedModel}
            />
            {/* Second tier below `md`: the session-folder chip, then the thinking
                and agent selectors, on a full-width line (or two) of their own.
                The chip belongs HERE and not on the primary line: the primary line
                is `+` (folder picker) · attach · model · send, and every one of
                those but the model is a fixed 44px touch target, so a chip there
                made the model trigger the only shrinkable item — measured with a
                folder set, before this arrangement: 26px wide with a 0px value
                span at 360 (a blank pill) and 56px with a 20px span at 390.
                Three cases below `md`:
                  - no folder  => one tier line, thinking + agent side by side;
                  - folder     => the chip takes a line of its own (`basis-full`,
                    hence the wrapper's `flex-wrap`), thinking + agent share the
                    next one, so the chip can never eat their width — sharing the
                    line with them crushed the pair to 50px/64px at 320, where the
                    rendered labels read `Def…` / `Main A…`;
                  - 768px up   => the wrapper is `md:contents` and the chip wrapper
                    is `md:hidden`, so everything collapses back onto the single
                    desktop row.
                The `+` button stays on the primary line because it is the control
                a finger reaches for. The tier exists whenever it has something to
                hold. */}
            {(thinkingEnabled || agentsOptions.length > 1 || Boolean(sessionFolder)) && (
              <div className="max-md:flex max-md:min-w-0 max-md:basis-full max-md:flex-wrap max-md:items-center max-md:gap-2 md:contents">
                {sessionFolder && (
                  <div className="max-md:basis-full md:hidden">
                    <FolderChip
                      folder={sessionFolder}
                      onClear={onClearFolder}
                      className="min-w-0 max-md:max-w-[200px]"
                    />
                  </div>
                )}
                {thinkingEnabled && (
                  <SearchableSelect
                    className="max-md:min-w-0"
                    ariaLabel={t('chat.thinking')}
                    buttonLabel={t('chat.thinking')}
                    direction="up"
                    emptyLabel={t('chat.thinkingOff')}
                    onChange={onSelectThinkLevel}
                    options={thinkOptions}
                    placeholder={
                      thinkOptions.find((o) => o.value === thinkLevel)?.label ??
                      t('chat.thinkingOff')
                    }
                    searchAriaLabel={`${t('chat.thinking')} ${t('chat.search')}`}
                    searchPlaceholder={t('chat.thinking')}
                    value={thinkLevel}
                  />
                )}
                {agentsOptions.length > 1 && (
                  <SearchableSelect
                    className="max-md:min-w-0"
                    ariaLabel={t('chat.agent')}
                    buttonLabel={t('chat.agent')}
                    disabled={hasConversation}
                    emptyLabel={t('chat.agentLocked')}
                    onChange={onSelectAgent}
                    options={agentsOptions}
                    placeholder={
                      agentsOptions.find((a) => a.value === selectedAgentId)?.label ??
                      t('chat.agent')
                    }
                    searchAriaLabel={`${t('chat.agent')} ${t('chat.search')}`}
                    searchPlaceholder={t('chat.agent')}
                    value={selectedAgentId}
                  />
                )}
              </div>
            )}
            {/* Token-speed chip: sits just before the send button (ml-auto pins
                it right; the button keeps its own ml-auto so it stays right when
                the chip is hidden). Hidden entirely when there is no valid
                rate, so it never displaces the controls or renders "0.0 tok/s". */}
            <StreamMetricsChip className="ml-auto" />
          </div>
          <button
            type={canCancel ? 'button' : 'submit'}
            disabled={false}
            aria-label={canCancel ? t('chat.cancel') : t('chat.send')}
            className={`ml-auto flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md transition-colors max-md:h-11 max-md:w-11 ${
              canCancel
                ? 'bg-state-error/10 text-state-error hover:bg-state-error-fill hover:text-text-on-accent border border-state-error/30'
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
