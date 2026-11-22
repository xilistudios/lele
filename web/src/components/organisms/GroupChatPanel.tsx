import { memo, useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { GroupInfo, GroupTurn } from '../../lib/types'
import { IconButton } from '../atoms/IconButton'
import { CloseIcon } from '../atoms/Icons'
import { SideDrawer } from '../atoms/SideDrawer'
import { MarkdownText } from '../molecules/MarkdownText'

interface GroupChatPanelProps {
  groups: GroupInfo[]
  isOpen: boolean
  onClose: () => void
}

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
      return 'bg-background-tertiary text-text-tertiary'
  }
}

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
        className="flex items-center justify-between w-full px-3 py-2 bg-surface-hover text-left hover:bg-background-elevated transition-colors"
      >
        <span className="text-xs font-medium text-text-secondary">
          {t('groups.layer', { number: layer + 1 })}
        </span>
        <span className="text-2xs text-text-tertiary">{open ? '▾' : '▸'}</span>
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

function TurnItem({ turn }: { turn: GroupTurn }) {
  const { t } = useTranslation()

  return (
    <div className="px-3 py-2">
      <div className="flex items-center gap-2 mb-1">
        <span className="text-xs font-medium text-text-primary">{turn.label}</span>
        <span
          className={`inline-flex items-center px-1.5 py-0.5 rounded text-2xs font-medium ${getRoleBadgeClass(turn.role)}`}
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

function GroupDetail({ group }: { group: GroupInfo }) {
  const { t } = useTranslation()

  const turnsByLayer = new Map<number, GroupTurn[]>()
  for (const turn of group.turns) {
    const arr = turnsByLayer.get(turn.layer) ?? []
    arr.push(turn)
    turnsByLayer.set(turn.layer, arr)
  }
  const sortedLayers = Array.from(turnsByLayer.keys()).sort((a, b) => a - b)

  const participantList = group.participants
    ? group.participants
        .split(',')
        .map((p) => p.trim())
        .filter(Boolean)
    : []

  return (
    <div className="flex flex-col gap-3">
      {participantList.length > 0 && (
        <div>
          <h4 className="text-xs font-medium text-text-secondary mb-1.5">
            {t('groups.participants')}
          </h4>
          <div className="flex flex-wrap gap-1.5">
            {participantList.map((name) => (
              <span
                key={name}
                className="inline-flex items-center px-2 py-0.5 rounded-sm bg-background-tertiary border border-border text-xs"
              >
                {name}
              </span>
            ))}
          </div>
        </div>
      )}

      {sortedLayers.map((layer) => (
        <LayerSection
          key={layer}
          layer={layer}
          turns={turnsByLayer.get(layer) ?? []}
          defaultOpen={layer === sortedLayers[sortedLayers.length - 1]}
        />
      ))}

      {group.synthesis && (
        <div className="border border-brand-rosa/30 rounded-md overflow-hidden">
          <div className="px-3 py-2 bg-brand-rosa/5">
            <span className="text-xs font-medium text-accent-text">
              {t('groups.finalSynthesis')}
            </span>
          </div>
          <div className="px-3 py-2 text-sm text-text-primary prose-sm prose-p:my-1">
            <MarkdownText content={group.synthesis} />
          </div>
        </div>
      )}

      <div className="flex items-center gap-3 text-xs text-text-tertiary pt-1">
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

function GroupStatusBadge({ status }: { status: string }) {
  const { t } = useTranslation()
  const colorClass =
    status === 'started'
      ? 'bg-state-info/15 text-state-info'
      : status === 'done'
        ? 'bg-state-success/15 text-state-success'
        : status === 'error'
          ? 'bg-state-error/15 text-state-error'
          : 'bg-background-tertiary text-text-tertiary'

  return (
    <span
      className={`inline-flex items-center px-1.5 py-0.5 rounded text-2xs font-medium ${colorClass}`}
    >
      {t(`groups.status.${status}`)}
    </span>
  )
}

export const GroupChatPanel = memo(function GroupChatPanel({
  groups,
  isOpen,
  onClose,
}: GroupChatPanelProps) {
  const { t } = useTranslation()
  const [selectedGroupId, setSelectedGroupId] = useState<string | null>(null)

  const selectedGroup = selectedGroupId
    ? groups.find((g) => g.groupID === selectedGroupId)
    : groups.length === 1
      ? groups[0]
      : null

  const handleClose = useCallback(() => {
    setSelectedGroupId(null)
    onClose()
  }, [onClose])

  return (
    <SideDrawer
      isOpen={isOpen}
      onClose={handleClose}
      ariaLabel={t('groups.title')}
      widthClassName="w-96"
      testId="group-chat-panel"
    >
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

      <div className="flex-1 overflow-y-auto">
        {groups.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-full text-text-tertiary">
            <p className="text-sm">{t('groups.noGroups')}</p>
          </div>
        ) : (
          <div className="p-4 space-y-4">
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
                        <p className="text-xs text-text-tertiary mt-0.5 truncate">
                          {group.participants}
                        </p>
                      )}
                    </button>
                  </li>
                ))}
              </ul>
            )}

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
    </SideDrawer>
  )
})
