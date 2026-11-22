import { useTranslation } from 'react-i18next'
import { useLocation, useNavigate } from 'react-router-dom'
import { IconButton } from './IconButton'
import { ChevronLeftIcon } from './Icons'

type Props = {
  label?: string
  fallbackTo?: string
  className?: string
}

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
