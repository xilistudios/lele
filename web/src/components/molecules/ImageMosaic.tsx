import { type ReactNode, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { isImageAttachment } from '../../lib/attachments'
import type { Attachment } from '../../lib/types'
import { AuthedImage } from './AuthedImage'
import { ImageLightbox } from './ImageLightbox'

type Props = {
  attachments: Attachment[]
  className?: string
  renderTileOverlay?: (attachment: Attachment, index: number) => ReactNode
}

function gridColumns(count: number): string {
  if (count <= 1) return 'grid-cols-1'
  if (count === 2) return 'grid-cols-2'
  return 'grid-cols-2 sm:grid-cols-3'
}

function tileClasses(count: number): string {
  const shape = count <= 1 ? 'max-h-96' : 'aspect-square'
  return `group relative block w-full overflow-hidden rounded-lg border border-border bg-background-secondary ${shape}`
}

function tileImageClasses(count: number): string {
  if (count <= 1) return 'h-full w-full object-contain'
  return 'h-full w-full object-cover transition-transform duration-200 group-hover:scale-[1.02]'
}

export function ImageMosaic({ attachments, className = '', renderTileOverlay }: Props) {
  const { t } = useTranslation()
  const [openIndex, setOpenIndex] = useState<number | null>(null)
  const images = attachments.filter(isImageAttachment)

  if (images.length === 0) return null

  const count = images.length
  const closeLightbox = () => setOpenIndex(null)

  return (
    <div
      data-testid="image-mosaic"
      data-count={count}
      className={`grid gap-1.5 ${gridColumns(count)} ${className}`.trim()}
    >
      {images.map((attachment, index) => {
        const label = attachment.name ?? attachment.path ?? 'image'
        return (
          <div key={`${attachment.path ?? label}:${index}`} className="relative">
            <button
              type="button"
              data-testid="image-mosaic-tile"
              aria-label={t('chat.viewImage')}
              title={t('chat.viewImage')}
              onClick={() => setOpenIndex(index)}
              className={tileClasses(count)}
            >
              <AuthedImage
                path={attachment.path ?? ''}
                alt={attachment.name ?? 'image'}
                className={tileImageClasses(count)}
              />
            </button>
            {renderTileOverlay ? (
              <div className="absolute right-1.5 top-1.5">
                {renderTileOverlay(attachment, index)}
              </div>
            ) : null}
          </div>
        )
      })}
      {openIndex !== null ? (
        <ImageLightbox
          images={images}
          index={openIndex}
          onClose={closeLightbox}
          onIndexChange={setOpenIndex}
        />
      ) : null}
    </div>
  )
}
