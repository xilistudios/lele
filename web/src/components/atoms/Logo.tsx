const LOGO_CONFIG = ['L', 'E', 'L', 'E'] as const

const DROP_SHADOW = 'drop-shadow-[1px_1px_0_rgba(0,0,0,0.8)]'

export function Logo({ collapsed = false }: { collapsed?: boolean }) {
  if (collapsed) {
    return (
      <span
        className={`text-lg font-bold uppercase tracking-wider ${DROP_SHADOW}`}
        style={{ color: '#E6D6EA' }}
      >
        L
      </span>
    )
  }

  return (
    <span className="text-lg font-bold uppercase tracking-wider">
      {LOGO_CONFIG.map((letter) => (
        <span key={letter} className={DROP_SHADOW} style={{ color: '#E6D6EA' }}>
          {letter}
        </span>
      ))}
    </span>
  )
}
