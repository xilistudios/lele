import { useMemo } from 'react'

/**
 * Agent avatar atom (spec §3.6 / §7.7).
 *
 * A decorative square with the agent's initial over a tinted background.
 * Per spec §2.6, the old multi-gradient palette (brand/secondary) was
 * replaced by a single `bg-accent-tint` — the avatar is decorative
 * identity (§2.2), not semantic, so colour variation is not required.
 */

/** Stable string hash (same algorithm the existing avatars use). */
export function agentIdHash(id: string): number {
  return [...id].reduce((acc, char) => (acc * 31 + char.charCodeAt(0)) >>> 0, 7)
}

/**
 * Background class for an agent avatar.
 *
 * Kept as a function for backward-compat with tests and previews that
 * import it.  Every id now maps to the same accent-tint token.
 */
export function gradientForId(_id: string): string {
  return 'bg-accent-tint'
}

type Size = 'sm' | 'md' | 'xl'

/** px box + text size + radius per size (§7.7: 20 / 40 / 48). */
const SIZE_CLASSES: Record<Size, string> = {
  sm: 'h-5 w-5 text-2xs rounded-md',
  md: 'h-10 w-10 text-sm rounded-lg',
  xl: 'h-12 w-12 text-lg rounded-xl',
}

type Props = {
  /** Agent id — the hash input, and the fallback initial source. */
  id: string
  /** Display name; its initial wins over the id's when present. */
  name?: string
  size?: Size
  className?: string
}

export function AgentAvatar({ id, name, size = 'md', className = '' }: Props) {
  const bg = useMemo(() => gradientForId(id), [id])
  const initial = (name?.trim() || id.trim()).charAt(0).toUpperCase()

  return (
    <span
      aria-hidden="true"
      className={`flex flex-none items-center justify-center border border-border font-semibold text-text-on-accent ${SIZE_CLASSES[size]} ${bg} ${className}`}
    >
      {initial}
    </span>
  )
}
