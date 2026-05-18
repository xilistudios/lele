import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { CheckIcon, CodeIcon, CopyIcon, EyeIcon } from '../atoms/Icons'

const MAX_HEIGHT = 1200
const MIN_HEIGHT = 500
const LOAD_TIMEOUT = 1000

type Props = {
  content: string
  language: 'html' | 'svg'
}

function buildSrcdoc(raw: string, lang: 'html' | 'svg'): string {
  const base =
    '<!DOCTYPE html><html><head><meta charset="utf-8"><style>body{margin:0;}</style></head><body>'
  const suffix = '</body></html>'
  if (lang === 'svg') {
    return `${base}<div style="display:flex;align-items:center;justify-content:center;min-height:100vh">${raw}</div>${suffix}`
  }
  return `${base}${raw}${suffix}`
}

export function CanvasBlock({ content, language }: Props) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<'preview' | 'code'>('preview')
  const [height, setHeight] = useState(MIN_HEIGHT)
  const [loadState, setLoadState] = useState<'loading' | 'loaded' | 'error'>('loading')
  const [copied, setCopied] = useState(false)
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const copyTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const resizeObserverRef = useRef<ResizeObserver | null>(null)
  const loadTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const measureHeight = useCallback(() => {
    try {
      const iframe = iframeRef.current
      if (!iframe?.contentDocument?.body) return
      const scrollH = iframe.contentDocument.body.scrollHeight
      if (scrollH > 0) {
        setHeight(Math.min(scrollH + 4, MAX_HEIGHT))
        setLoadState('loaded')
      }
    } catch {
      setLoadState('error')
    }
  }, [])

  const setupResizeObserver = useCallback(() => {
    const iframe = iframeRef.current
    if (!iframe?.contentDocument?.body) return
    resizeObserverRef.current?.disconnect()
    const observer = new ResizeObserver(() => measureHeight())
    observer.observe(iframe.contentDocument.body)
    resizeObserverRef.current = observer
  }, [measureHeight])

  useEffect(() => {
    return () => {
      if (copyTimeoutRef.current) clearTimeout(copyTimeoutRef.current)
      if (loadTimeoutRef.current) clearTimeout(loadTimeoutRef.current)
      resizeObserverRef.current?.disconnect()
    }
  }, [])

  // Fallback: if onLoad never fires, force loaded after timeout
  useEffect(() => {
    if (loadState !== 'loading') return
    loadTimeoutRef.current = setTimeout(() => {
      setLoadState('loaded')
      setHeight(MIN_HEIGHT)
    }, LOAD_TIMEOUT)
    return () => {
      if (loadTimeoutRef.current) clearTimeout(loadTimeoutRef.current)
    }
  }, [loadState])

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(content)
    } catch {
      // Clipboard not available
    }
    setCopied(true)
    if (copyTimeoutRef.current) clearTimeout(copyTimeoutRef.current)
    copyTimeoutRef.current = setTimeout(() => setCopied(false), 2000)
  }, [content])

  const handleIframeLoad = useCallback(() => {
    measureHeight()
    setupResizeObserver()
  }, [measureHeight, setupResizeObserver])

  if (!content.trim()) {
    return (
      <div className="rounded-lg border border-border bg-background-primary overflow-hidden">
        <div className="flex items-center border-b border-border px-3 py-1.5">
          <span className="rounded px-1.5 py-0.5 bg-surface-hover text-[10px] font-mono text-text-tertiary">
            {language}
          </span>
        </div>
        <div className="flex items-center justify-center px-4 py-8 text-xs text-text-tertiary">
          {t('canvas.noContent')}
        </div>
      </div>
    )
  }

  const srcdoc = buildSrcdoc(content, language)

  return (
    <div className="rounded-lg border border-border bg-background-primary overflow-hidden">
      <div className="flex items-center justify-between border-b border-border px-3 py-1.5">
        <span className="rounded px-1.5 py-0.5 bg-surface-hover text-[10px] font-mono text-text-tertiary">
          {language}
        </span>
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={() => setMode(mode === 'preview' ? 'code' : 'preview')}
            className="flex items-center gap-1.5 rounded px-2 py-1 text-[11px] text-text-tertiary transition-colors hover:bg-surface-hover hover:text-text-secondary"
            title={mode === 'preview' ? t('canvas.viewCode') : t('canvas.viewPreview')}
          >
            {mode === 'preview' ? <CodeIcon size={12} /> : <EyeIcon size={12} />}
            <span>{mode === 'preview' ? t('canvas.code') : t('canvas.preview')}</span>
          </button>
          <button
            type="button"
            onClick={handleCopy}
            className="flex items-center gap-1.5 rounded px-2 py-1 text-[11px] text-text-tertiary transition-colors hover:bg-surface-hover hover:text-text-secondary"
            title={t('canvas.copyCode')}
          >
            {copied ? <CheckIcon size={12} /> : <CopyIcon size={12} />}
            <span>{copied ? t('canvas.copied') : t('canvas.copy')}</span>
          </button>
        </div>
      </div>

      {mode === 'preview' ? (
        <div className="bg-white relative min-h-[80px]">
          {loadState === 'loading' && (
            <div className="absolute inset-0 z-10 flex items-center justify-center bg-white/90">
              <div className="flex items-center gap-2 text-xs text-text-tertiary">
                <span className="inline-block h-2 w-2 rounded-full bg-text-tertiary animate-pulse" />
                <span className="inline-block h-2 w-2 rounded-full bg-text-tertiary animate-pulse [animation-delay:0.2s]" />
                <span className="inline-block h-2 w-2 rounded-full bg-text-tertiary animate-pulse [animation-delay:0.4s]" />
                <span className="ml-1">{t('canvas.loadingPreview')}</span>
              </div>
            </div>
          )}
          {loadState === 'error' && (
            <div className="flex items-center justify-center px-4 py-8 text-xs text-state-error">
              {t('canvas.renderError')}
            </div>
          )}
          <iframe
            ref={iframeRef}
            srcDoc={srcdoc}
            sandbox="allow-scripts"
            title={t('canvas.previewTitle')}
            onLoad={handleIframeLoad}
            style={{
              height: `${height}px`,
              maxHeight: `${MAX_HEIGHT}px`,
            }}
            className="w-full border-0"
          />
        </div>
      ) : (
        <pre className="overflow-x-auto px-4 py-3 text-xs text-text-secondary font-mono leading-5 max-h-[600px] overflow-y-auto">
          <code>{content}</code>
        </pre>
      )}
    </div>
  )
}
