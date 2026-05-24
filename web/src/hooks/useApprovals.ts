import { useCallback, useRef, useState } from 'react'
import type { ApprovalRequest, ApprovalResult } from '../lib/types'

const APPROVAL_RESULT_DISPLAY_MS = 5000

/**
 * Manages approval request/result state for command approval flows.
 * Results auto-clear after a timeout.
 */
export function useApprovals() {
  const [approvalRequest, setApprovalRequest] = useState<ApprovalRequest | null>(null)
  const [approvalResult, setApprovalResult] = useState<ApprovalResult | null>(null)
  const timerRef = useRef<ReturnType<typeof setTimeout>>()

  const clearTimer = useCallback(() => {
    if (timerRef.current) clearTimeout(timerRef.current)
  }, [])

  const showResult = useCallback(
    (requestId: string, approved: boolean, command: string) => {
      setApprovalRequest(null)
      clearTimer()
      setApprovalResult({ requestId, approved, command })
      timerRef.current = setTimeout(() => setApprovalResult(null), APPROVAL_RESULT_DISPLAY_MS)
    },
    [clearTimer],
  )

  const approveRequest = useCallback(
    (approved: boolean, requestId: string, command: string) => {
      showResult(requestId, approved, command)
    },
    [showResult],
  )

  const clear = useCallback(() => {
    setApprovalRequest(null)
    clearTimer()
    setApprovalResult(null)
  }, [clearTimer])

  return {
    approvalRequest,
    setApprovalRequest,
    approvalResult,
    approveRequest,
    showResult,
    clear,
  }
}
