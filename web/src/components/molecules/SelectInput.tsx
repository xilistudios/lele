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
      className="w-full rounded border border-[#3a3a3a] bg-[#1a1a1a] px-3 py-2 text-xs text-[#e0e0e0] focus:border-blue-500 focus:outline-none disabled:opacity-50"
    >
      {options.map((opt) => (
        <option key={opt.value} value={opt.value}>
          {opt.label}
        </option>
      ))}
    </select>
  )
}
