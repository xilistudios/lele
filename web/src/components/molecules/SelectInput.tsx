type Option = {
  value: string
  label: string
}

type Props = {
  id: string
  value: string
  onChange: (value: string) => void
  options: Option[]
  disabled?: boolean
}

export function SelectInput({ id, value, onChange, options, disabled }: Props) {
  return (
    <select
      id={id}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={disabled}
      className="w-full rounded border border-border bg-background-primary px-3 py-2 text-xs text-text-primary focus:border-blue-500 focus:outline-none disabled:opacity-50"
    >
      {options.map((opt) => (
        <option key={opt.value} value={opt.value}>
          {opt.label}
        </option>
      ))}
    </select>
  )
}
