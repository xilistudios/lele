import { useTranslation } from 'react-i18next'
import { AddButton } from '../atoms/AddButton'

type Props = {
  value: string
  onChange: (value: string) => void
  onAdd: () => void
  placeholder: string
  disabled?: boolean
}

const INPUT_CLS =
  'w-full rounded border border-[#3a3a3a] bg-[#1a1a1a] px-3 py-2 text-xs text-[#e0e0e0] placeholder-[#555] focus:border-blue-500 focus:outline-none disabled:opacity-50'

export function AddItemInput({ value, onChange, onAdd, placeholder, disabled }: Props) {
  const { t } = useTranslation()

  return (
    <div className="mb-4 flex gap-2">
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault()
            onAdd()
          }
        }}
        placeholder={placeholder}
        disabled={disabled}
        className={INPUT_CLS}
      />
      <AddButton onClick={onAdd} disabled={!value.trim()}>
        {t('common.add')}
      </AddButton>
    </div>
  )
}
