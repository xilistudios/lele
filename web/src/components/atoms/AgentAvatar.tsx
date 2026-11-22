import { useMemo } from 'react'

export function agentIdHash(id: string): number {
  return [...id].reduce((acc, char) => (acc * 31 + char.charCodeAt(0)) >>> 0, 7)
}

export function gradientForId(_id: string): string {
  return 'bg-accent-tint'
}

type Size = 'sm' | 'md' | 'xl'

const SIZE_CLASSES: Record<Size, string> = {
  sm: 'h-5 w-5 text-2xs rounded-md',
  md: 'h-10 w-10 text-sm rounded-lg',
  xl: 'h-12 w-12 text-lg rounded-xl',
}

type Props = {
  id: string
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
