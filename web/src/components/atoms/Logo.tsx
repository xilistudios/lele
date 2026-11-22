const LOGO_CONFIG = ['L', 'E', 'L', 'E'] as const

export function Logo({ collapsed = false }: { collapsed?: boolean }) {
  if (collapsed) {
    return (
      <span className="text-lg font-semibold uppercase tracking-[0.02em] text-brand-rosa">L</span>
    )
  }

  return (
    <span className="text-lg font-semibold uppercase tracking-[0.02em] text-text-primary">
      {LOGO_CONFIG.map((letter, index) => (
        <span key={`${letter}-${index}`} className={index === 0 ? 'text-brand-rosa' : undefined}>
          {letter}
        </span>
      ))}
    </span>
  )
}
