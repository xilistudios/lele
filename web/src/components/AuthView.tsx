import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import type { FormEvent } from 'react'

type Props = {
  apiUrl: string
  error: string | null
  onSubmit: (input: { apiUrl: string; pin: string; deviceName: string }) => Promise<void>
}

export function AuthView({ apiUrl, error, onSubmit }: Props) {
  const { t } = useTranslation()
  const [apiInput, setApiInput] = useState(apiUrl)
  const [pin, setPin] = useState('')
  const [deviceName, setDeviceName] = useState('My Desktop')
  const [loading, setLoading] = useState(false)
  const disabled =
    apiInput.trim().length === 0 || pin.trim().length !== 6 || deviceName.trim().length === 0 || loading

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (disabled) {
      return
    }

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
        className="w-full max-w-md space-y-5 rounded-3xl border border-slate-800 bg-slate-900/80 p-6 shadow-2xl shadow-sky-950/30"
        onSubmit={handleSubmit}
      >
        <div className="space-y-2">
          <p className="text-sm uppercase tracking-[0.3em] text-sky-400">{t('auth.subtitle')}</p>
          <h1 className="text-3xl font-semibold text-white">{t('auth.title')}</h1>
        </div>

        <div className="rounded-2xl border border-sky-500/30 bg-sky-500/5 p-4">
          <label className="block space-y-2">
            <span className="text-sm font-medium text-slate-100">{t('auth.apiUrlLabel')}</span>
            <input
              className="w-full rounded-2xl border border-slate-700 bg-slate-950 px-4 py-3 text-white outline-none ring-0 placeholder:text-slate-500 focus:border-sky-500"
              placeholder={t('auth.apiUrlPlaceholder')}
              value={apiInput}
              onChange={(event) => setApiInput(event.target.value)}
            />
            <p className="text-xs text-slate-400">{t('auth.apiUrlHint')}</p>
          </label>
        </div>

        <label className="block space-y-2">
          <span className="text-sm text-slate-300">{t('auth.pinLabel')}</span>
          <input
            className="w-full rounded-2xl border border-slate-700 bg-slate-950 px-4 py-3 text-white outline-none ring-0 placeholder:text-slate-500 focus:border-sky-500"
            inputMode="numeric"
            maxLength={6}
            placeholder={t('auth.pinPlaceholder')}
            value={pin}
            onChange={(event) => setPin(event.target.value)}
          />
        </label>

        <label className="block space-y-2">
          <span className="text-sm text-slate-300">{t('auth.deviceNameLabel')}</span>
          <input
            className="w-full rounded-2xl border border-slate-700 bg-slate-950 px-4 py-3 text-white outline-none ring-0 placeholder:text-slate-500 focus:border-sky-500"
            placeholder={t('auth.deviceNamePlaceholder')}
            value={deviceName}
            onChange={(event) => setDeviceName(event.target.value)}
          />
        </label>

        {error ? (
          <p className="rounded-2xl border border-rose-900 bg-rose-950/60 px-4 py-3 text-sm text-rose-200">
            {error}
          </p>
        ) : null}

        <button
          className="w-full rounded-2xl bg-sky-500 px-4 py-3 font-medium text-slate-950 transition hover:bg-sky-400 disabled:cursor-not-allowed disabled:opacity-50"
          disabled={disabled}
          type="submit"
        >
          {loading ? t('auth.connecting') : t('auth.connect')}
        </button>
      </form>
    </main>
  )
}
