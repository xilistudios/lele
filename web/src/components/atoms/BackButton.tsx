import { useTranslation } from 'react-i18next'
import { useLocation, useNavigate } from 'react-router-dom'
import { IconButton } from './IconButton'
import { ChevronLeftIcon } from './Icons'

type Props = {
  /** Accessible label / tooltip. Defaults to `common.back` ("Atrás"). */
  label?: string
  /**
   * Where to go when there is no in-app history to pop (the page was opened
   * directly, e.g. a pasted link or a reload). Defaults to the chat.
   */
  fallbackTo?: string
  className?: string
}

/**
 * "Back" affordance for the pages that are only reachable from the chat's
 * Tools drawer (skills, scheduled jobs, background processes, secrets).
 *
 * They have no sidebar entry any more, so the header needs an explicit way
 * out. It pops the history when there is somewhere to pop to — that returns to
 * the chat *and* to the agent tab when the page was reached from an agent
 * (`/skills?view=agent&agent=…`) — and falls back to `fallbackTo` when the page
 * was opened cold, where `navigate(-1)` would do nothing.
 *
 * The initial entry of a router session always has `key === 'default'`, which
 * is what tells the two cases apart without touching `window.history`.
 */
export function BackButton({ label, fallbackTo = '/', className = '' }: Props) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const location = useLocation()

  const text = label ?? t('common.back')

  const handleBack = () => {
    if (location.key === 'default') {
      navigate(fallbackTo, { replace: true })
      return
    }
    navigate(-1)
  }

  return (
    <IconButton
      onClick={handleBack}
      ariaLabel={text}
      title={text}
      variant="ghost"
      dataTestId="back-button"
      className={`flex h-8 w-8 flex-none items-center justify-center ${className}`}
    >
      <ChevronLeftIcon size={18} />
    </IconButton>
  )
}
