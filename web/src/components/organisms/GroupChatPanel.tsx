import { memo, useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { GroupInfo, GroupTurn } from '../../lib/types'
import { IconButton } from '../atoms/IconButton'
import { CloseIcon } from '../atoms/Icons'
import { MarkdownText } from '../molecules/MarkdownText'

interface GroupChatPanelProps {
  groups: GroupInfo[]
  isOpen: boolean
  onClose: () => void
}

const ANIMATION_DURATION_MS = 300

// ── Role badge colors ───────────────────────────────────────────────────────

function getRoleBadgeClass(role: string): string {
  switch (role) {
    case 'proposer':
      return 'bg-state-info/15 text-state-info'
    case 'aggregator':
      return 'bg-state-success/15 text-state-success'
    case 'moderator':
      return 'bg-state-warning/15 text-state-warning'
    case 'critic':
      return 'bg-state-error/15 text-state-error'
    default:
      return 'bg-surface-card text-text-tertiary'
  }
}

// ── Layer section ────────────────────────────────────────────────────────────

function LayerSection({
  layer,
  turns,
  defaultOpen,
}: {
  layer: number
  turns: GroupTurn[]
  defaultOpen: boolean
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(defaultOpen)

  return (
    <div className="border border-border rounded-md overflow-hidden">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center justify-between w-full px-3 py-2 bg-surface-hover text-left hover:bg-surface-card transition-colors"
      >
        <span className="text-xs font-medium text-text-secondary">
          {t('groups.layer', { number: layer + 1 })}
        </span>
        <span className="text-[10px] text-text-tertiary">{open ? '▾' : '▸'}</span>
      </button>
      {open && (
        <div className="divide-y divide-border">
          {turns.map((turn) => (
            <TurnItem key={turn.turnIndex} turn={turn} />
          ))}
        </div>
      )}
    </div>
  )
}

// ── Single turn ──────────────────────────────────────────────────────────────

function TurnItem({ turn }: { turn: GroupTurn }) {
  const { t } = useTranslation()

  return (
    <div className="px-3 py-2">
      <div className="flex items-center gap-2 mb-1">
        <span className="text-xs font-medium text-text-primary">{turn.label}</span>
        <span
          className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${getRoleBadgeClass(turn.role)}`}
        >
          {t(`groups.role.${turn.role}`)}
        </span>
      </div>
      <div className="text-sm text-text-secondary prose-sm prose-p:my-1">
        <MarkdownText content={turn.content} />
      </div>
    </div>
  )
}

// ── Group detail ─────────────────────────────────────────────────────────────

function GroupDetail({ group }: { group: GroupInfo }) {
  const { t } = useTranslation()

  // Group turns by layer
  const turnsByLayer = new Map<number, GroupTurn[]>()
  for (const turn of group.turns) {
    const arr = turnsByLayer.get(turn.layer) ?? []
    arr.push(turn)
    turnsByLayer.set(turn.layer, arr)
  }
  const sortedLayers = Array.from(turnsByLayer.keys()).sort((a, b) => a - b)

  // Parse participants
  const participantList = group.participants
    ? group.participants
        .split(',')
        .map((p) => p.trim())
        .filter(Boolean)
    : []

  return (
    <div className="flex flex-col gap-3">
      {/* Participants */}
      {participantList.length > 0 && (
        <div>
          <h4 className="text-xs font-medium text-text-secondary mb-1.5">
            {t('groups.participants')}
          </h4>
          <div className="flex flex-wrap gap-1.5">
            {participantList.map((name) => (
              <span
                key={name}
                className="inline-flex items-center px-2 py-0.5 rounded text-[11px] bg-surface-card text-text-secondary border border-border"
              >
                {name}
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Turns grouped by layer */}
      {sortedLayers.map((layer) => (
        <LayerSection
          key={layer}
          layer={layer}
          turns={turnsByLayer.get(layer) ?? []}
          defaultOpen={layer === sortedLayers[sortedLayers.length - 1]}
        />
      ))}

      {/* Synthesis */}
      {group.synthesis && (
        <div className="border border-brand-rosa/30 rounded-md overflow-hidden">
          <div className="px-3 py-2 bg-brand-rosa/5">
            <span className="text-xs font-medium text-brand-rosa">
              {t('groups.finalSynthesis')}
            </span>
          </div>
          <div className="px-3 py-2 text-sm text-text-primary prose-sm prose-p:my-1">
            <MarkdownText content={group.synthesis} />
          </div>
        </div>
      )}

      {/* Stats footer */}
      <div className="flex items-center gap-3 text-[11px] text-text-tertiary pt-1">
        {group.strategy && (
          <span>
            {t('groups.strategy')}: {group.strategy}
          </span>
        )}
        {group.totalTokens > 0 && (
          <span>
            {group.totalTokens.toLocaleString()} {t('groups.tokens')}
          </span>
        )}
      </div>
    </div>
  )
}

// ── Status badge ─────────────────────────────────────────────────────────────

function GroupStatusBadge({ status }: { status: string }) {
  const { t } = useTranslation()
  const colorClass =
    status === 'started'
      ? 'bg-state-info/15 text-state-info'
      : status === 'done'
        ? 'bg-state-success/15 text-state-success'
        : status === 'error'
          ? 'bg-state-error/15 text-state-error'
          : 'bg-surface-card text-text-tertiary'

  return (
    <span
      className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${colorClass}`}
    >
      {t(`groups.status.${status}`)}
    </span>
  )
}

// ── Main panel ───────────────────────────────────────────────────────────────

export const GroupChatPanel = memo(function GroupChatPanel({
  groups,
  isOpen,
  onClose,
}: GroupChatPanelProps) {
  const { t } = useTranslation()
  const [visible, setVisible] = useState(false)
  const [animate, setAnimate] = useState(false)
  const [selectedGroupId, setSelectedGroupId] = useState<string | null>(null)
  const rafRef = useRef<number>(0)

  useEffect(() => {
    if (isOpen) {
      setVisible(true)
      rafRef.current = requestAnimationFrame(() => {
        rafRef.current = requestAnimationFrame(() => setAnimate(true))
      })
      return () => cancelAnimationFrame(rafRef.current)
    }
    setAnimate(false)
    const timer = setTimeout(() => setVisible(false), ANIMATION_DURATION_MS)
    return () => {
      clearTimeout(timer)
      cancelAnimationFrame(rafRef.current)
    }
  }, [isOpen])

  const selectedGroup = selectedGroupId
    ? groups.find((g) => g.groupID === selectedGroupId)
    : groups.length === 1
      ? groups[0]
      : null

  const handleClose = useCallback(() => {
    setSelectedGroupId(null)
    onClose()
  }, [onClose])

  if (!visible) return null

  return (
    <>
      {/* Backdrop */}
      <button
        type="button"
        className={`fixed inset-0 z-40 bg-black/40 transition-opacity duration-300 ease-out ${animate ? 'opacity-100' : 'opacity-0 pointer-events-none'}`}
        onClick={handleClose}
        aria-label={t('common.close')}
      />

      {/* Panel */}
      <div
        className={`fixed right-0 top-0 z-50 h-full w-96 max-w-[90vw] bg-background-primary border-l border-border shadow-lg flex flex-col transition-transform duration-300 ease-out ${animate ? 'translate-x-0' : 'translate-x-full'}`}
      >
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <h3 className="text-sm font-medium text-text-primary">{t('groups.title')}</h3>
          <IconButton
            onClick={handleClose}
            className="rounded p-1 text-text-tertiary hover:bg-surface-hover hover:text-text-secondary transition-colors"
            aria-label={t('common.close')}
          >
            <CloseIcon size={16} />
          </IconButton>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto">
          {groups.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-full text-text-tertiary">
              <p className="text-sm">{t('groups.noGroups')}</p>
            </div>
          ) : (
            <div className="p-4 space-y-4">
              {/* Group list (when multiple groups) */}
              {groups.length > 1 && !selectedGroup && (
                <ul className="divide-y divide-border border border-border rounded-md overflow-hidden">
                  {groups.map((group) => (
                    <li key={group.groupID}>
                      <button
                        type="button"
                        onClick={() => setSelectedGroupId(group.groupID)}
                        className="w-full px-3 py-2.5 text-left hover:bg-surface-hover transition-colors"
                      >
                        <div className="flex items-center justify-between">
                          <span className="text-sm font-medium text-text-primary truncate">
                            {group.groupID}
                          </span>
                          <GroupStatusBadge status={group.status} />
                        </div>
                        {group.participants && (
                          <p className="text-[11px] text-text-tertiary mt-0.5 truncate">
                            {group.participants}
                          </p>
                        )}
                      </button>
                    </li>
                  ))}
                </ul>
              )}

              {/* Single group detail */}
              {selectedGroup && (
                <>
                  {groups.length > 1 && (
                    <button
                      type="button"
                      onClick={() => setSelectedGroupId(null)}
                      className="text-xs text-text-tertiary hover:text-text-secondary transition-colors"
                    >
                      ← {t('groups.backToList')}
                    </button>
                  )}
                  <div className="flex items-center gap-2 mb-1">
                    <h4 className="text-sm font-medium text-text-primary truncate">
                      {selectedGroup.groupID}
                    </h4>
                    <GroupStatusBadge status={selectedGroup.status} />
                  </div>
                  <GroupDetail group={selectedGroup} />
                </>
              )}
            </div>
          )}
        </div>
      </div>
    </>
  )
})
