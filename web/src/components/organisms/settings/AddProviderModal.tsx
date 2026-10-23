import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSettings } from '../../../contexts/SettingsContext'
import { detectProviderFromKey } from '../../../lib/keyPrefix'
import type { CatalogProvider, EditableNamedProviderConfig, SecretValue } from '../../../lib/types'
import { getProviderDisplayName } from '../../../lib/utils'
import { Modal } from '../../atoms'
import { CheckIcon, ChevronRightIcon } from '../../atoms/Icons'
import { BooleanInput, SecretInput } from '../../molecules'

// Source of truth for the Add-provider screen: the selectable types (curated,
// in muscle-memory order) and the api_base pre-filled when a tile is picked.
// Both are exported for tests.
export const PROVIDER_TYPES = [
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'openrouter', label: 'OpenRouter' },
  { value: 'groq', label: 'Groq' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'deepseek', label: 'DeepSeek' },
  { value: 'ollama', label: 'Ollama' },
  { value: 'vllm', label: 'vLLM' },
  { value: 'nvidia', label: 'NVIDIA' },
  { value: 'moonshot', label: 'Moonshot' },
  { value: 'zhipu', label: 'Zhipu' },
  { value: 'nanogpt', label: 'NanoGPT' },
  { value: 'chutes', label: 'Chutes' },
  { value: 'alibaba', label: 'Alibaba' },
  { value: 'alibaba_coding_plan', label: 'Alibaba Coding Plan' },
  { value: 'shengsuanyun', label: 'ShengSuanYun' },
  { value: 'zai_coding_plan', label: 'zAI Coding Plan' },
  { value: 'modelark_coding_plan', label: 'ModelArk Coding Plan' },
  { value: 'github_copilot', label: 'GitHub Copilot' },
  { value: 'opencode', label: 'OpenCode Zen' },
  { value: 'opencode_go', label: 'OpenCode Go' },
  { value: 'claude-code', label: 'Claude Code (CLI)' },
  { value: 'codex-cli', label: 'Codex CLI' },
  { value: 'custom', label: 'OpenAI Compatible' },
]

export const DEFAULT_API_BASE: Record<string, string> = {
  anthropic: 'https://api.anthropic.com/v1',
  openai: 'https://api.openai.com/v1',
  openrouter: 'https://openrouter.ai/api/v1',
  groq: 'https://api.groq.com/openai/v1',
  gemini: 'https://generativelanguage.googleapis.com/v1beta',
  deepseek: 'https://api.deepseek.com/v1',
  ollama: 'http://localhost:11434/v1',
  vllm: '',
  nvidia: 'https://integrate.api.nvidia.com/v1',
  moonshot: 'https://api.moonshot.cn/v1',
  zhipu: 'https://open.bigmodel.cn/api/paas/v4',
  nanogpt: 'https://nano-gpt.com/api/v1',
  chutes: 'https://llm.chutes.ai/v1',
  alibaba: 'https://coding-intl.dashscope.aliyuncs.com/v1',
  alibaba_coding_plan: 'https://coding-intl.dashscope.aliyuncs.com/v1',
  shengsuanyun: 'https://router.shengsuanyun.com/api/v1',
  // `zai` has no static tile: under the curated-first merge below, this value
  // wins for the catalog entry (both carry the same URL today). It pairs with
  // the coding plan below and must stay distinct from it: the coding plan
  // lives under /api/coding/, NOT /api/.
  zai: 'https://api.z.ai/api/paas/v4',
  zai_coding_plan: 'https://api.z.ai/api/coding/paas/v4',
  modelark_coding_plan: 'https://ark.ap-southeast.bytepluses.com/api/coding/v3',
  github_copilot: 'localhost:4321',
  opencode: 'https://opencode.ai/zen/v1',
  opencode_go: 'https://opencode.ai/zen/go/v1',
  'claude-code': '',
  'codex-cli': '',
  custom: '',
}

/**
 * The key field starts in `literal` mode so `#new-provider-api-key` is in the DOM
 * the moment the modal opens (SecretInput renders no input at all in `empty`
 * mode). A blank literal is normalised back to `empty` at add time, see
 * `handleAdd`.
 */
const INITIAL_API_KEY: SecretValue = { mode: 'literal', value: '', has_env_var: false }

/**
 * One chooser tile. Derived from the static list plus the (optional) catalog,
 * never stored in state.
 */
type ProviderTile = {
  /** The config provider type: a static value or a catalog `id`. */
  type: string
  label: string
  apiBase: string
  modelCount?: number
  source: 'static' | 'catalog' | 'both' | 'detected'
}

type Props = {
  isOpen: boolean
  onClose: () => void
}

/** `Claude Code (CLI)` -> `claude-code-cli`; `''` when nothing alphanumeric is left. */
function toSlug(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

/** Append `-2`, `-3`, ... until `base` is free among `existingNames`. */
function dedupeName(base: string, existingNames: string[]): string {
  const taken = new Set(existingNames)
  let name = base
  let suffix = 2
  while (taken.has(name)) {
    name = `${base}-${suffix}`
    suffix += 1
  }
  return name
}

/**
 * Keep only `[A-Za-z0-9_-]` from a typed provider name. A `.` would split the
 * dotted path (`providers.<name>.api_base`) that `setValueAtPath` walks on the
 * next edit of this provider (useSettingsConfig.ts), silently corrupting the
 * entry; the other dropped characters are removed, not replaced.
 */
function sanitizeProviderName(raw: string): string {
  return raw.replace(/[^A-Za-z0-9_-]/g, '').trim()
}

/**
 * Derive the config key (`providers.<name>`) for a new provider: slug the *type*
 * (the stable identifier the user will see next to the entry in ProvidersSettings),
 * fall back to the label, then to `provider`, and de-duplicate against the names
 * already in the draft with a `-2`, `-3` suffix.
 *
 * Pure so it can be unit-tested. Note the dedupe runs against *every* existing
 * key, including the empty stubs a shipped config carries: adding Anthropic on
 * top of an existing `anthropic` stub yields `anthropic-2` rather than silently
 * overwriting it.
 *
 * The type wins over the label on purpose: `('claude-code', 'Claude Code (CLI)')`
 * must yield `claude-code`, not `claude-code-cli`. For the `custom` tile the
 * caller passes `effectiveType` — the user-typed type — so a private gateway
 * gets its own name instead of the "OpenAI Compatible" brand slug.
 */
export function uniqueProviderName(type: string, label: string, existingNames: string[]): string {
  const slug = toSlug(type) || toSlug(label) || 'provider'
  return dedupeName(slug, existingNames)
}

export function AddProviderModal({ isOpen, onClose }: Props) {
  const { api, draftConfig, updateField, t } = useSettings()

  const [query, setQuery] = useState('')
  // A key prefix only *mirrors* a selection: until the user clicks a tile the
  // selection is `detectProviderFromKey(key)`, so clearing the key clears the
  // tile too. A manual click freezes the choice for the rest of the session.
  const [pickedType, setPickedType] = useState('')
  const [userPicked, setUserPicked] = useState(false)
  const [customType, setCustomType] = useState('')
  const [apiKey, setApiKey] = useState<SecretValue>(INITIAL_API_KEY)
  const [nameDraft, setNameDraft] = useState('')
  const [apiBase, setApiBase] = useState('')
  const [apiBaseEdited, setApiBaseEdited] = useState(false)
  const [proxy, setProxy] = useState('')
  const [webSearch, setWebSearch] = useState(false)
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [catalog, setCatalog] = useState<CatalogProvider[]>([])
  // SecretInput is remounted (via React `key`) whenever a tile is clicked: React
  // only honours `autoFocus` at mount, so bumping this counter is what actually
  // moves the caret into the key field.
  const [keyFieldMount, setKeyFieldMount] = useState(0)

  const searchRef = useRef<HTMLInputElement>(null)

  // The catalog only enriches the static list; a failure is silent because the
  // static tiles are enough to add a provider. ApiClient takes no AbortSignal,
  // hence the `cancelled` flag for resolves that land after close.
  useEffect(() => {
    if (!isOpen) return
    let cancelled = false
    api
      .catalogProviders()
      .then((res) => {
        if (!cancelled) setCatalog(res.providers ?? [])
      })
      .catch(() => {
        /* silent by contract: the static list stays */
      })
    return () => {
      cancelled = true
    }
  }, [isOpen, api])

  // Only setters inside, so the identity is stable and the effect below can list it.
  const resetForm = useCallback(() => {
    setQuery('')
    setPickedType('')
    setUserPicked(false)
    setCustomType('')
    setApiKey(INITIAL_API_KEY)
    setNameDraft('')
    setApiBase('')
    setApiBaseEdited(false)
    setProxy('')
    setWebSearch(false)
    setAdvancedOpen(false)
    setKeyFieldMount(0)
  }, [])

  // Reset on open as well as on close: a session cancelled via Escape/backdrop
  // from a parent that keeps the component mounted must not leak its edits.
  useEffect(() => {
    if (!isOpen) return
    resetForm()
    searchRef.current?.focus()
  }, [isOpen, resetForm])

  // Derived first because the tile grid can depend on it (detected-tile
  // synthesis below). A key prefix only *mirrors* a selection until the user
  // clicks a tile; `userPicked` freezes it for the rest of the session.
  const selectedType = userPicked ? pickedType : detectProviderFromKey(apiKey.value ?? '') || ''
  const effectiveType = selectedType === 'custom' ? customType.trim() : selectedType

  const tiles = useMemo<ProviderTile[]>(() => {
    const byType = new Map<string, ProviderTile>()
    for (const pt of PROVIDER_TYPES) {
      byType.set(pt.value, {
        type: pt.value,
        label: pt.label,
        apiBase: DEFAULT_API_BASE[pt.value] ?? '',
        source: 'static',
      })
    }
    // The catalog `id` IS the config provider type. Its `type` field is only a
    // transport hint and is never read here.
    //
    // Precedence: the curated static values WIN, the catalog only fills gaps —
    // `label` = static label -> catalog name -> display-name fallback;
    // `apiBase` = non-empty DEFAULT_API_BASE -> catalog api_base. The catalog
    // still supplies `modelCount`, and everything for catalog-only ids (that
    // is the point of the merge). Catalog-first was a regression: the curated
    // `github_copilot` base (`localhost:4321`) is the gRPC CLI bridge passed to
    // `copilot.ClientOptions{CLIUrl}`, while the catalog's
    // `https://api.githubcopilot.com/v1` is an HTTP endpoint — feeding it to
    // the provider makes it unable to start. Labels drift the same way (the
    // catalog says `OpenCode`/`Google` where the curated list says
    // `OpenCode Zen`/`Gemini`).
    for (const provider of catalog) {
      // A blank id would make a tile with an empty `data-provider-type`, and an
      // empty tile type matches the unselected `selectedType === ''` — the tile
      // would render permanently pressed. There is no provider to add without
      // an id, so the entry is dropped instead.
      if (!provider.id) continue
      const current = byType.get(provider.id)
      byType.set(provider.id, {
        type: provider.id,
        label: current?.label || provider.name || getProviderDisplayName(provider.id),
        apiBase: DEFAULT_API_BASE[provider.id] || provider.api_base || '',
        modelCount:
          provider.model_count && provider.model_count > 0
            ? provider.model_count
            : current?.modelCount,
        source: current ? 'both' : 'catalog',
      })
    }
    // A key-detected provider can outpace the grid: `xai`/`perplexity` are
    // catalog-only, so while the fetch is pending (or failed) there is no tile
    // to light up even though Add is enabled. Synthesize one right after the
    // static tiles so the selection is visible and filterable; once the
    // catalog supplies the real entry the merge above wins and this branch
    // stops firing — deduped through the same map, never a second tile.
    if (selectedType !== '' && !byType.has(selectedType)) {
      byType.set(selectedType, {
        type: selectedType,
        label: getProviderDisplayName(selectedType),
        apiBase: DEFAULT_API_BASE[selectedType] ?? '',
        source: 'detected',
      })
    }
    const curated = [...byType.values()].filter((tile) => tile.source !== 'catalog')
    const discovered = [...byType.values()]
      .filter((tile) => tile.source === 'catalog')
      .sort((a, b) => a.label.localeCompare(b.label))
    return [...curated, ...discovered]
  }, [catalog, selectedType])

  const filteredTiles = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return tiles
    return tiles.filter(
      (tile) => tile.label.toLowerCase().includes(q) || tile.type.toLowerCase().includes(q),
    )
  }, [tiles, query])

  const selectedTile = tiles.find((tile) => tile.type === selectedType)
  // The API key never gates Add: keyless local/CLI providers are first class and
  // the backend decides at use time.
  //
  // Never let Add act on a tile the filter hid: a selection that is not in
  // `filteredTiles` is invisible, so the button would write a provider the user
  // cannot see (paste an Anthropic key, filter to `groq`, click Add ->
  // Anthropic). Gated on the *tile*, not on `effectiveType`, because the custom
  // tile's written type is the user-typed string and never appears in the grid.
  const canAdd =
    !!draftConfig &&
    effectiveType !== '' &&
    filteredTiles.some((tile) => tile.type === selectedType)

  // The name the add would use while the Advanced field is left empty. Shown
  // as that field's placeholder so the live default is visible (spec §2.5).
  const providersDraft = draftConfig?.providers ?? {}
  const tileLabel =
    selectedType === 'custom' ? effectiveType : (selectedTile?.label ?? effectiveType)
  const derivedName = uniqueProviderName(effectiveType, tileLabel, Object.keys(providersDraft))

  // api_base follows the tile until the user types their own, so switching tiles
  // cannot clobber a hand-written endpoint.
  useEffect(() => {
    if (apiBaseEdited) return
    setApiBase(selectedTile?.apiBase ?? '')
  }, [selectedTile, apiBaseEdited])

  const handleSelectTile = (tile: ProviderTile) => {
    setUserPicked(true)
    setPickedType(tile.type)
    setKeyFieldMount((n) => n + 1)
  }

  const handleSecretChange = (next: SecretValue) => {
    // SecretInput clears `value` when the mode flips (literal -> env/empty),
    // which would silently drop a key-detected selection mid-flow and grey
    // out Add. A mode change therefore freezes the current selection; typing
    // within one mode keeps the mirror (C8: clearing the text still clears a
    // detected pick).
    if (next.mode !== apiKey.mode && selectedType !== '') {
      setUserPicked(true)
      setPickedType(selectedType)
    }
    setApiKey(next)
  }

  const handleClose = () => {
    resetForm()
    onClose()
  }

  const handleAdd = () => {
    if (!canAdd || !draftConfig) return

    // Normalise before writing: ProvidersSettings classifies a provider as empty
    // by `api_key.mode === 'empty'`, so a blank literal/env must be persisted as
    // "no secret" instead of an empty secret.
    const secret: SecretValue =
      apiKey.mode === 'literal' && (apiKey.value ?? '').trim() !== ''
        ? { mode: 'literal', value: apiKey.value, has_env_var: false }
        : apiKey.mode === 'env' && (apiKey.env_name ?? '').trim() !== ''
          ? { mode: 'env', env_name: apiKey.env_name, has_env_var: true }
          : { mode: 'empty', has_env_var: false }

    // A typed draft is sanitized (a dot would corrupt the dotted paths the
    // next ProvidersSettings edit walks) and then deduped with the same
    // -2/-3 rule as the derived name, so typing an existing provider's name
    // creates `anthropic-2` instead of silently overwriting `anthropic`.
    // A draft that sanitizes to nothing falls back to the derived name.
    const draftName = sanitizeProviderName(nameDraft)
    const name = draftName ? dedupeName(draftName, Object.keys(providersDraft)) : derivedName

    const newProvider: EditableNamedProviderConfig = {
      type: effectiveType,
      api_key: secret,
      api_base: apiBase,
      proxy: proxy || undefined,
      web_search: webSearch,
    }

    updateField('providers', {
      ...providersDraft,
      [name]: newProvider,
    })

    handleClose()
  }

  const INPUT_CLS =
    'w-full rounded-lg border border-border bg-background-tertiary px-3.5 py-2.5 text-xs text-text-primary placeholder-text-tertiary focus:border-focus focus:outline-none transition-all duration-fast focus:ring-2 focus:ring-focus/20'
  const BTN_CLS =
    'rounded-lg px-4 py-2.5 text-xs font-medium transition-all duration-fast active:scale-[0.98] disabled:opacity-40 disabled:cursor-not-allowed'
  const BTN_PRIMARY = `${BTN_CLS} bg-accent-primary text-text-on-accent hover:bg-accent-hover`
  const BTN_SECONDARY = `${BTN_CLS} bg-background-tertiary text-text-secondary hover:bg-surface-hover`

  return (
    <Modal
      isOpen={isOpen}
      onClose={handleClose}
      title={t('settings.addProviderModal.title')}
      size="lg"
    >
      <div className="space-y-4 p-6">
        {/* The tile grid owns the only scrollbar (R3), so the modal body itself does not scroll in the normal case and the filter needs no sticky. */}
        <div className="-mx-6 -mt-6 mb-2 border-b border-border bg-background-primary px-6 py-3">
          <input
            ref={searchRef}
            type="text"
            aria-label={t('settings.addProviderModal.searchPlaceholder')}
            placeholder={t('settings.addProviderModal.searchPlaceholder')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className={INPUT_CLS}
          />
        </div>

        {/* biome-ignore lint/a11y/useSemanticElements: a tile chooser is not a form field — <fieldset> would add form semantics, a legend and UA borders to a modal that never submits */}
        <div
          role="group"
          aria-label={t('settings.fields.providerType')}
          className={`grid grid-cols-2 gap-2 overflow-y-auto pr-1 ${advancedOpen ? 'max-h-[24vh]' : 'max-h-[38vh]'}`}
        >
          {filteredTiles.map((tile) => {
            const selected = tile.type === selectedType
            return (
              <button
                key={tile.type}
                type="button"
                aria-pressed={selected}
                data-provider-type={tile.type}
                onClick={() => handleSelectTile(tile)}
                className={`flex items-start justify-between gap-2 rounded-lg border p-3 text-left transition-all duration-fast ${
                  selected
                    ? 'border-focus ring-2 ring-focus/20 bg-focus/10'
                    : 'border-border bg-background-tertiary hover:bg-surface-hover'
                }`}
              >
                <span className="min-w-0">
                  <span className="block truncate text-sm font-medium text-text-primary">
                    {tile.label}
                  </span>
                  <span className="block truncate text-2xs text-text-tertiary">
                    {tile.type}
                    {tile.modelCount
                      ? ` · ${t('settings.addProviderModal.modelsCount', { count: tile.modelCount })}`
                      : ''}
                  </span>
                </span>
                {selected && (
                  <span className="flex-none text-state-info">
                    <CheckIcon size={14} />
                  </span>
                )}
              </button>
            )
          })}
        </div>

        {filteredTiles.length === 0 && (
          <p className="py-6 text-center text-xs text-text-tertiary">
            {t('settings.addProviderModal.noResults')}
          </p>
        )}

        {selectedType === 'custom' && (
          <div>
            <label
              htmlFor="new-provider-custom-type"
              className="mb-1 block text-xs text-text-secondary"
            >
              {t('settings.addProviderModal.customTypeLabel')}
            </label>
            <input
              id="new-provider-custom-type"
              type="text"
              value={customType}
              onChange={(e) => setCustomType(e.target.value)}
              placeholder={t('settings.addProviderModal.customTypePlaceholder')}
              className={INPUT_CLS}
            />
          </div>
        )}

        <div>
          <label htmlFor="new-provider-api-key" className="mb-1 block text-xs text-text-secondary">
            {t('settings.fields.providerApiKey')}
          </label>
          <SecretInput
            key={keyFieldMount}
            id="new-provider-api-key"
            autoFocus={keyFieldMount > 0}
            value={apiKey}
            onChange={handleSecretChange}
            placeholder="sk-..."
          />
          <div id="new-provider-api-key-hint" className="mt-1 flex flex-wrap items-center gap-2">
            <p className="text-xs text-text-tertiary">
              {t('settings.addProviderModal.apiKeyHint')}
            </p>
            {/* The key field sits below the grid, so a key-detected tile can be
                off-screen when it lights up: name the detected provider right
                here instead. No new locale key — the label is the tile's own. */}
            {selectedType !== '' && (
              <span
                data-detected-provider={selectedType}
                className="rounded-full bg-state-info/10 px-1.5 py-0.5 text-2xs font-medium text-state-info"
              >
                {selectedTile?.label ?? getProviderDisplayName(effectiveType)}
              </span>
            )}
          </div>
        </div>

        <div>
          <button
            type="button"
            aria-expanded={advancedOpen}
            aria-controls="new-provider-advanced"
            onClick={() => setAdvancedOpen(!advancedOpen)}
            className="flex items-center gap-1.5 text-xs font-medium text-text-secondary hover:text-text-primary"
          >
            <ChevronRightIcon
              size={12}
              className={`transition-transform duration-fast ${advancedOpen ? 'rotate-90' : ''}`}
            />
            {t('settings.addProviderModal.advanced')}
          </button>

          {advancedOpen && (
            <div id="new-provider-advanced" className="mt-3 space-y-3 animate-step-enter">
              <div>
                <label
                  htmlFor="new-provider-name"
                  className="mb-1 block text-xs text-text-secondary"
                >
                  {t('settings.addProviderModal.nameLabel')}
                </label>
                <input
                  id="new-provider-name"
                  type="text"
                  value={nameDraft}
                  onChange={(e) => setNameDraft(e.target.value)}
                  placeholder={derivedName}
                  className={INPUT_CLS}
                />
                <p className="mt-1 text-xs text-text-tertiary">
                  {t('settings.addProviderModal.nameHint')}
                </p>
              </div>

              <div>
                <label
                  htmlFor="new-provider-api-base"
                  className="mb-1 block text-xs text-text-secondary"
                >
                  {t('settings.fields.providerApiBase')}
                </label>
                <input
                  id="new-provider-api-base"
                  type="text"
                  value={apiBase}
                  onChange={(e) => {
                    // Once the user owns the value, tile switches must not touch it.
                    setApiBaseEdited(true)
                    setApiBase(e.target.value)
                  }}
                  className={INPUT_CLS}
                />
              </div>

              <div>
                <label
                  htmlFor="new-provider-proxy"
                  className="mb-1 block text-xs text-text-secondary"
                >
                  {t('settings.fields.providerProxy')}
                </label>
                <input
                  id="new-provider-proxy"
                  type="text"
                  value={proxy}
                  onChange={(e) => setProxy(e.target.value)}
                  className={INPUT_CLS}
                />
              </div>

              <div>
                <label
                  htmlFor="new-provider-web-search"
                  className="mb-1 block text-xs text-text-secondary"
                >
                  {t('settings.fields.providerWebSearch')}
                </label>
                <BooleanInput
                  id="new-provider-web-search"
                  value={webSearch}
                  onChange={setWebSearch}
                />
              </div>
            </div>
          )}
        </div>

        <div className="flex justify-end gap-2 pt-2">
          <button type="button" onClick={handleClose} className={BTN_SECONDARY}>
            {t('common.cancel')}
          </button>
          <button type="button" onClick={handleAdd} disabled={!canAdd} className={BTN_PRIMARY}>
            {t('settings.addProviderModal.add')}
          </button>
        </div>
      </div>
    </Modal>
  )
}
