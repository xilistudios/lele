import { type ChangeEvent, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { IconButton } from '../atoms/IconButton'

type Props = {
  onUpload: (files: File[]) => Promise<string[]>
  onAttach: (paths: string[]) => void
}

export function AttachmentInput({ onUpload, onAttach }: Props) {
  const { t } = useTranslation()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [error, setError] = useState<string | null>(null)

  const handleAttachmentInput = async (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? [])
    if (files.length === 0) return

    try {
      const paths = await onUpload(files)
      // onUpload may swallow the API error and return [] — treat that as a
      // failure so the user is not left wondering why nothing was attached.
      if (paths.length === 0) {
        setError(t('chat.uploadFailed'))
      } else {
        setError(null)
        onAttach(paths)
      }
    } catch (err) {
      console.error('Upload failed:', err)
      setError(t('chat.uploadFailed'))
    }

    event.target.value = ''
  }

  return (
    <div className="relative">
      <input
        ref={fileInputRef}
        className="hidden"
        multiple
        type="file"
        onChange={handleAttachmentInput}
      />
      <IconButton
        onClick={() => fileInputRef.current?.click()}
        title={t('chat.attachFiles')}
        ariaLabel={t('chat.attachFiles')}
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          aria-hidden="true"
        >
          <path d="M21.44 11.05 12.25 20.24a6 6 0 0 1-8.49-8.49l9.2-9.19a4 4 0 1 1 5.65 5.66l-9.2 9.19a2 2 0 1 1-2.82-2.83l8.48-8.48" />
        </svg>
      </IconButton>
      {error && (
        <p
          role="alert"
          className="absolute left-0 top-full z-10 mt-1 whitespace-nowrap rounded-md border border-state-error/30 bg-state-error-light px-2 py-1 text-[10px] text-state-error shadow-sm"
        >
          {error}
        </p>
      )}
    </div>
  )
}
