import { useTranslation } from 'react-i18next'
import type { ApprovalRequest } from '../../lib/types'

type Props = {
  request: ApprovalRequest
  onApprove: () => void
  onReject: () => void
}

export function ApprovalInline({ request, onApprove, onReject }: Props) {
  const { t } = useTranslation()
  return (
    <div className="py-3 animate-in">
      <div className="rounded-lg border border-state-warning/30 bg-state-warning-light px-4 py-3">
        <div className="flex items-start gap-3">
          <div className="mt-0.5 flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-full bg-state-warning/20">
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              className="text-state-warning"
              aria-hidden="true"
            >
              <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
              <line x1="12" y1="9" x2="12" y2="13" />
              <line x1="12" y1="17" x2="12.01" y2="17" />
            </svg>
          </div>
          <div className="min-w-0 flex-1">
            <p className="text-xs font-medium uppercase tracking-widest text-state-warning">
              {t('approval.inlineTitle')}
            </p>
            <p className="mt-1 text-sm text-state-warning/70">{t('approval.pendingCommand')}</p>
            <pre className="mt-3 rounded border border-border bg-background-primary px-3 py-2.5 font-mono text-xs text-text-secondary overflow-x-auto">
              {request.command}
            </pre>
            {request.reason && <p className="mt-2 text-sm text-text-secondary">{request.reason}</p>}
            <div className="mt-4 flex gap-3">
              <button
                className="flex-1 rounded-md border border-border px-4 py-2 text-sm text-text-secondary hover:bg-surface-hover transition-colors"
                onClick={onReject}
                type="button"
              >
                {t('approval.reject')}
              </button>
              <button
                className="flex-1 rounded-md bg-state-success px-4 py-2 text-sm font-medium text-text-on-accent hover:bg-state-success/80 transition-colors"
                onClick={onApprove}
                type="button"
              >
                {t('approval.approve')}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
