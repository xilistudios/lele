type Props = {
  id: string
  value: number
  onChange: (value: number) => void
  disabled?: boolean
  min?: number
  max?: number
  step?: number
}

export function NumberInput({ id, value, onChange, disabled, min, max, step = 1 }: Props) {
  return (
    <input
      id={id}
      type="number"
      value={value}
      onChange={(e) => onChange(Number(e.target.value))}
      disabled={disabled}
      min={min}
      max={max}
      step={step}
      className="w-full rounded border border-border bg-background-primary px-3 py-2 text-xs text-text-primary placeholder:text-text-tertiary focus:border-blue-500 focus:outline-none disabled:opacity-50"
    />
  )
}
