type Props = {
  id: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
  disabled?: boolean
  type?: 'text' | 'number' | 'password'
  min?: number
  max?: number
}

export function TextInput({
  id,
  value,
  onChange,
  placeholder,
  disabled,
  type = 'text',
  min,
  max,
}: Props) {
  return (
    <input
      id={id}
      type={type}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={disabled}
      placeholder={placeholder}
      min={min}
      max={max}
      className="w-full rounded border border-border bg-background-primary px-3 py-2 text-xs text-text-primary placeholder:text-text-tertiary focus:border-blue-500 focus:outline-none disabled:opacity-50"
    />
  )
}
