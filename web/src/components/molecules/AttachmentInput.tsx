import { type ChangeEvent, useRef } from 'react'
import { IconButton } from '../atoms/IconButton'

type Props = {
  onUpload: (files: File[]) => Promise<string[]>
  onAttach: (paths: string[]) => void
}

export function AttachmentInput({ onUpload, onAttach }: Props) {
  const fileInputRef = useRef<HTMLInputElement>(null)

  const handleAttachmentInput = async (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? [])
    if (files.length === 0) return

    try {
      const paths = await onUpload(files)
      onAttach(paths)
    } catch (err) {
      console.error('Upload failed:', err)
    }

    event.target.value = ''
  }

  return (
    <>
      <input
        ref={fileInputRef}
        className="hidden"
        multiple
        type="file"
        onChange={handleAttachmentInput}
      />
      <IconButton
        onClick={() => fileInputRef.current?.click()}
        title="Attach files"
        ariaLabel="Attach files"
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
    </>
  )
}
