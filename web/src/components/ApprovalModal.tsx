import { useTranslation } from 'react-i18next'
import type { ApprovalRequest } from '../lib/types'

type Props = {
  request: ApprovalRequest
  onApprove: () => void
  onReject: () => void
}

export function ApprovalModal({ request, onApprove, onReject }: Props) {
  const { t } = useTranslation()
  return (
    <div className="fixed inset-0 z-20 flex items-center justify-center bg-black/60 px-4 backdrop-blur-sm">
      <div className="w-full max-w-lg rounded-lg border border-[#3a3a3a] bg-[#222] p-5 shadow-2xl">
        <p className="text-xs font-medium uppercase tracking-widest text-amber-400">
          {t('approval.title')}
        </p>
        <h2 className="mt-2 text-base font-medium text-white">{t('approval.pendingCommand')}</h2>
        <pre className="mt-4 rounded border border-[#3a3a3a] bg-[#1a1a1a] px-4 py-3 font-mono text-xs text-[#ccc] overflow-x-auto">
          {request.command}
        </pre>
        {request.reason && (
          <p className="mt-3 text-sm text-[#888]">{request.reason}</p>
        )}

        <div className="mt-5 flex gap-3">
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
  )
}
