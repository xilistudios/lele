// Desktop environment detection for the Tauri shell.

/** True when running inside the Tauri desktop shell (as opposed to a plain browser). */
export function isDesktop(): boolean {
  return typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window
}

/** True when the desktop shell injected an auth token (via window.__LELE_DESKTOP__). */
export function isDesktopAuth(): boolean {
  if (typeof window === 'undefined') return false
  const desktop = (window as any).__LELE_DESKTOP__
  return Boolean(desktop?.token ?? desktop?.session?.token)
}

/** The gateway URL injected by the desktop shell, if any. */
export function getDesktopApiUrl(): string | null {
  if (typeof window === 'undefined') return null
  return (window as any).__LELE_DESKTOP__?.apiUrl ?? null
}

/**
 * Invoke a Tauri IPC command. Returns null when not running inside the
 * desktop shell or when the command fails (graceful degradation — the web
 * build must keep working in a plain browser).
 */
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