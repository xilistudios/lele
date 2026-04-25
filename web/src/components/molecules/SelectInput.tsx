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
      className="w-full rounded border border-border bg-background-primary px-3 py-2 text-xs text-text-primary focus:border-interaction-primary focus:outline-none focus:ring-2 focus:ring-interaction-primary focus:ring-offset-2 focus:ring-offset-background-primary disabled:opacity-40"
    >
      {options.map((opt) => (
        <option key={opt.value} value={opt.value}>
          {opt.label}
        </option>
      ))}
    </select>
  )
}
