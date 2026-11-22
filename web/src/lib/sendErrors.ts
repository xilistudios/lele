import i18n from '../i18n'

export const SEND_ERROR_I18N_KEYS: Record<string, string> = {
  content_missing: 'chat.errorContentMissing',
  attachments_rejected: 'chat.errorAttachmentsRejected',
  forbidden: 'chat.errorAccessDenied',
}

export function sendErrorMessage(code: string, serverMessage = ''): string {
  const key = SEND_ERROR_I18N_KEYS[code]
  if (key) return i18n.t(key)
  if (serverMessage) return serverMessage
  return i18n.t('chat.failedToSend')
}
