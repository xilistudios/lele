type Props = {
  onClick: () => void
  disabled?: boolean
  children: string
}

const ADD_BTN_CLS =
  'rounded bg-cta-primary px-3 py-2 text-xs text-text-on-accent transition-colors hover:bg-cta-hover disabled:opacity-40'

export function AddButton({ onClick, disabled = false, children }: Props) {
  return (
    <button type="button" onClick={onClick} disabled={disabled} className={ADD_BTN_CLS}>
      {children}
    </button>
  )
}
