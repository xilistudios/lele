type Props = {
  onClick: () => void
  ariaLabel: string
}

const REMOVE_BTN_CLS = 'text-rose-400 transition-colors hover:text-rose-300 disabled:opacity-50'

export function RemoveButton({ onClick, ariaLabel }: Props) {
  return (
    <button type="button" onClick={onClick} className={REMOVE_BTN_CLS} aria-label={ariaLabel}>
      <svg
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        aria-hidden="true"
      >
        <path d="M18 6L6 18M6 6l12 12" />
      </svg>
    </button>
  )
}
