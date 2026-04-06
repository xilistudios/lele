import { useTranslation } from 'react-i18next'
import type { ApprovalRequest } from '../lib/types'

type Props = {
  request: ApprovalRequest
  onApprove: () => void
  onReject: () => void
}

export function ApprovalInline({ request, onApprove, onReject }: Props) {
  const { t } = useTranslation()
  return (
    <div className="py-3 animate-in">
      <div className="rounded-lg border border-amber-800/50 bg-amber-950/20 px-4 py-3">
        <div className="flex items-start gap-3">
          <div className="mt-0.5 flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-full bg-amber-900/40">
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              className="text-amber-400"
              aria-hidden="true"
            >
              <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
              <line x1="12" y1="9" x2="12" y2="13" />
              <line x1="12" y1="17" x2="12.01" y2="17" />
            </svg>
          </div>
          <div className="min-w-0 flex-1">
            <p className="text-xs font-medium uppercase tracking-widest text-amber-400">
              {t('approval.inlineTitle')}
            </p>
            <p className="mt-1 text-sm text-amber-200/80">{t('approval.pendingCommand')}</p>
            <pre className="mt-3 rounded border border-amber-900/30 bg-[#1a1a1a] px-3 py-2.5 font-mono text-xs text-[#ccc] overflow-x-auto">
              {request.command}
            </pre>
            {request.reason && <p className="mt-2 text-sm text-[#888]">{request.reason}</p>}
            <div className="mt-4 flex gap-3">
              <button
                className="flex-1 rounded-md border border-[#3a3a3a] px-4 py-2 text-sm text-[#aaa] hover:bg-[#2a2a2a] transition-colors"
                onClick={onReject}
                type="button"
              >
                {t('approval.reject')}
              </button>
              <button
                className="flex-1 rounded-md bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-500 transition-colors"
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
