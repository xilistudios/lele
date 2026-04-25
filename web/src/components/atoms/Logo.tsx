const LOGO_CONFIG = [
  { letter: 'L', color: 'text-brand-rosa' },
  { letter: 'E', color: 'text-brand-turquesa' },
  { letter: 'L', color: 'text-brand-amarillo' },
  { letter: 'E', color: 'text-brand-rosa' },
] as const

const DROP_SHADOW = 'drop-shadow-[1px_1px_0_rgba(0,0,0,0.8)]'

export function Logo({ collapsed = false }: { collapsed?: boolean }) {
  if (collapsed) {
    return (
      <span
        className={`text-lg font-bold uppercase tracking-wider ${LOGO_CONFIG[0].color} ${DROP_SHADOW}`}
      >
        L
      </span>
    )
  }

  return (
    <span className="text-lg font-bold uppercase tracking-wider">
      {LOGO_CONFIG.map(({ letter, color }) => (
        <span key={letter + color} className={`${color} ${DROP_SHADOW}`}>
          {letter}
        </span>
      ))}
    </span>
  )
}
