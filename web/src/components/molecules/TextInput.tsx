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
      className="w-full rounded-md border border-border/60 bg-surface-tertiary px-4 py-2.5 text-sm text-text-primary placeholder:text-text-muted transition-all duration-150 hover:border-border/80 focus:border-interaction-primary focus:outline-none focus:ring-2 focus:ring-interaction-primary/20 focus:ring-offset-0 disabled:opacity-40"
    />
  )
}
