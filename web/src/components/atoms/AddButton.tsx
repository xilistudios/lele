type Props = {
  onClick: () => void
  disabled?: boolean
  children: string
}

const ADD_BTN_CLS =
  'rounded bg-blue-600 px-3 py-2 text-xs text-white transition-colors hover:bg-blue-500 disabled:opacity-50'

export function AddButton({ onClick, disabled = false, children }: Props) {
  return (
    <button type="button" onClick={onClick} disabled={disabled} className={ADD_BTN_CLS}>
      {children}
    </button>
  )
}
