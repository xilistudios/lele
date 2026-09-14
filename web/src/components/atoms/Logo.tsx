const LOGO_CONFIG = ['L', 'E', 'L', 'E'] as const

/**
 * §5.13 — wordmark en text-primary + primera letra brand-rosa (glifo decorativo
 * grande dentro de una palabra legible). Sin drop-shadow: la sombra
 * rgba(0,0,0,.8) estaba prohibida (mismo hex duplicado que Icons.tsx).
 */
export function Logo({ collapsed = false }: { collapsed?: boolean }) {
  if (collapsed) {
    return (
      <span className="text-lg font-semibold uppercase tracking-[0.02em] text-brand-rosa">
        L
      </span>
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
