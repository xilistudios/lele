export function isDesktop(): boolean {
  return typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window
}

export function isDesktopAuth(): boolean {
  if (typeof window === 'undefined') return false
  const desktop = (window as any).__LELE_DESKTOP__
  return Boolean(desktop?.token ?? desktop?.session?.token)
}

export function getDesktopApiUrl(): string | null {
  if (typeof window === 'undefined') return null
  return (window as any).__LELE_DESKTOP__?.apiUrl ?? null
}

export async function invokeDesktop<T>(
  command: string,
  args?: Record<string, unknown>,
): Promise<T | null> {
  if (!isDesktop()) return null
  try {
    const internals = (window as any).__TAURI_INTERNALS__
    if (!internals?.invoke) return null
    return (await internals.invoke(command, args)) as T
  } catch {
    return null
  }
}
