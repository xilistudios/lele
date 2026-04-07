type Props = {
  id: string
  value: number
  onChange: (value: number) => void
  disabled?: boolean
  min?: number
  max?: number
  step?: number
}

export function NumberInput({
  id,
  value,
  onChange,
  disabled,
  min,
  max,
  step = 1,
}: Props) {
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
      className="w-full rounded border border-[#3a3a3a] bg-[#1a1a1a] px-3 py-2 text-xs text-[#e0e0e0] placeholder-[#555] focus:border-blue-500 focus:outline-none disabled:opacity-50"
    />
  )
}
