let homeDir = ''

export function setHomeDir(home: string | undefined | null): void {
  homeDir = home ?? ''
}

export function getHomeDir(): string {
  return homeDir
}

export function expandHomeDisplay(path: string): string {
  if (!path || path[0] !== '~') return path
  if (!homeDir) return path
  if (path.length === 1) return homeDir
  if (path[1] === '/') return homeDir + path.slice(1)
  return path
}
