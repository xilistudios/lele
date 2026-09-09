/**
 * Path display helpers (spec §4.3 / §7.9).
 *
 * DISPLAY ONLY. The real `~` expansion happens on the backend
 * (`pkg/agent/instance.go: expandHome`, `pkg/config/config.go: expandHome`),
 * which resolves `~/…` against `os.UserHomeDir()`. The browser cannot know
 * that home directory, so nothing here is ever written back to the config:
 * the stored value keeps its `~` and the UI shows a resolved preview next to it.
 */

/**
 * Best-effort home directory used for the preview.
 *
 * The WebUI has no OS home available (it also runs in a plain browser), so we
 * expose a settable value instead of guessing: `FolderPickerModal` learns the
 * server-reported home from `FsListResponse.home` and the agents pages can
 * `setHomeDir()` it, which makes the preview real. Until then the helper is a
 * no-op and `~/lele` displays as `~/lele` — honest, never wrong.
 */
let homeDir = ''

/** Register the home directory to use for display (e.g. from the API). */
export function setHomeDir(home: string | undefined | null): void {
  homeDir = home ?? ''
}

/** Currently registered home directory for display purposes ('' = unknown). */
export function getHomeDir(): string {
  return homeDir
}

/**
 * Expand a leading `~` for display, mirroring the backend rule:
 * - `~` alone            -> home (unchanged when home is unknown)
 * - `~/sub/path`         -> `<home>/sub/path`
 * - anything else        -> unchanged (absolute paths, `~user`, …)
 *
 * `~` is intentionally kept as-is when no home has been registered: a literal
 * `~` prefix is still visually resolvable ("starts at home") and matches what
 * the config file actually contains.
 */
export function expandHomeDisplay(path: string): string {
  if (!path || path[0] !== '~') return path
  if (!homeDir) return path
  if (path.length === 1) return homeDir
  if (path[1] === '/') return homeDir + path.slice(1)
  // `~user/…` is not supported by the backend either: leave it untouched.
  return path
}
