import { useMemo } from 'react'

/**
 * Agent avatar atom (spec §3.6 / §7.7).
 *
 * A decorative square with the agent's initial over a gradient picked
 * deterministically from a hash of the agent id: the same agent always looks
 * the same, and deleting or reordering another agent never repaints it.
 *
 * Every gradient is composed exclusively of existing `brand-*` / `secondary-*`
 * / `interaction-*` tokens — no new colors are introduced (§9).
 */

const GRADIENTS = [
  'from-interaction-primary to-brand-morado', // turquesa → morado
  'from-brand-morado to-brand-rosa', // morado → rosa
  'from-brand-turquesa to-secondary-azul', // turquesa → azul
  'from-brand-naranja to-brand-rosa', // naranja → rosa
  'from-secondary-verde to-brand-turquesa', // verde → turquesa
  'from-brand-rosa to-brand-morado', // rosa → morado
  'from-secondary-azul to-brand-morado', // azul → morado
]

/** Stable string hash (same algorithm the existing avatars use). */
export function agentIdHash(id: string): number {
  return [...id].reduce((acc, char) => (acc * 31 + char.charCodeAt(0)) >>> 0, 7)
}

/** Gradient classes for an id (exported for tests / reuse in previews). */
export function gradientForId(id: string): string {
  return GRADIENTS[agentIdHash(id) % GRADIENTS.length]
}

type Size = 'sm' | 'md' | 'xl'

/** px box + text size + radius per size (§7.7: 20 / 40 / 48). */
const SIZE_CLASSES: Record<Size, string> = {
  sm: 'h-5 w-5 text-[10px] rounded-md',
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
  const gradient = useMemo(() => gradientForId(id), [id])
  const initial = (name?.trim() || id.trim()).charAt(0).toUpperCase()

  return (
    <span
      aria-hidden="true"
      className={`flex flex-none items-center justify-center bg-gradient-to-br font-semibold text-text-on-accent ${SIZE_CLASSES[size]} ${gradient} ${className}`}
    >
      {initial}
    </span>
  )
}
