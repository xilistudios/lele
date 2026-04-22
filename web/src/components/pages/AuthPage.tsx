import { type FormEvent, useState } from 'react'
import { useTranslation } from 'react-i18next'

type Props = {
  apiUrl: string
  error: string | null
  initialPin?: string
  onSubmit: (input: { apiUrl: string; pin: string; deviceName: string }) => Promise<void>
}

export function AuthPage({ apiUrl, error, initialPin = '', onSubmit }: Props) {
  const { t } = useTranslation()
  const [apiInput, setApiInput] = useState(apiUrl)
  const [pin, setPin] = useState(initialPin)
  const [deviceName, setDeviceName] = useState('My Desktop')
  const [loading, setLoading] = useState(false)
  const disabled =
    apiInput.trim().length === 0 ||
    pin.trim().length !== 6 ||
    deviceName.trim().length === 0 ||
    loading

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (disabled) return

    setLoading(true)
    try {
      await onSubmit({ apiUrl: apiInput.trim(), pin: pin.trim(), deviceName: deviceName.trim() })
    } finally {
      setLoading(false)
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center px-4 py-12">
      <form
        className="w-full max-w-md space-y-5 rounded-2xl border border-border bg-background-primary p-6 shadow-xl"
        onSubmit={handleSubmit}
      >
        <div className="space-y-2">
          <p className="text-sm uppercase tracking-wider text-brand-blue">{t('auth.subtitle')}</p>
          <h1 className="text-2xl font-semibold text-text-primary">{t('auth.title')}</h1>
        </div>

        <div className="rounded-xl border border-border-light bg-background-secondary p-4">
          <label className="block space-y-2">
            <span className="text-sm font-medium text-text-primary">{t('auth.apiUrlLabel')}</span>
            <input
              className="w-full rounded-lg border border-border bg-background-primary px-4 py-2.5 text-text-primary outline-none ring-0 placeholder:text-text-tertiary focus:border-border-focus"
              placeholder={t('auth.apiUrlPlaceholder')}
              value={apiInput}
              onChange={(event) => setApiInput(event.target.value)}
            />
            <p className="text-xs text-text-tertiary">{t('auth.apiUrlHint')}</p>
          </label>
        </div>

        <label className="block space-y-2">
          <span className="text-sm text-text-secondary">{t('auth.pinLabel')}</span>
          <input
            className="w-full rounded-lg border border-border bg-background-primary px-4 py-2.5 text-text-primary outline-none ring-0 placeholder:text-text-tertiary focus:border-border-focus"
            inputMode="numeric"
            maxLength={6}
            placeholder={t('auth.pinPlaceholder')}
            value={pin}
            onChange={(event) => setPin(event.target.value)}
          />
        </label>

        <label className="block space-y-2">
          <span className="text-sm text-text-secondary">{t('auth.deviceNameLabel')}</span>
          <input
            className="w-full rounded-lg border border-border bg-background-primary px-4 py-2.5 text-text-primary outline-none ring-0 placeholder:text-text-tertiary focus:border-border-focus"
            placeholder={t('auth.deviceNamePlaceholder')}
            value={deviceName}
            onChange={(event) => setDeviceName(event.target.value)}
          />
        </label>

        {error && (
          <p className="rounded-lg border border-border bg-state-error/20 px-4 py-2.5 text-sm text-state-error">
            {error}
          </p>
        )}

        <button
          className="w-full rounded-lg bg-brand-blue px-4 py-2.5 font-medium text-text-primary transition hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
          disabled={disabled}
          type="submit"
        >
          {loading ? t('auth.connecting') : t('auth.connect')}
        </button>
      </form>
    </main>
  )
}
