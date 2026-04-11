export const formatSessionTitle = (
  sessionKey: string,
  sessionName?: string,
  messageCount?: number,
) => {
  if (sessionName?.trim()) return sessionName
  if (!messageCount || messageCount === 0) return 'New Chat'
  const parts = sessionKey.split(':')
  return parts.length > 2 ? `Session ${parts[parts.length - 1]}` : sessionKey
}
