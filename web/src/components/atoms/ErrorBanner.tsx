type Props = {
  message: string
}

export function ErrorBanner({ message }: Props) {
  return (
    <div className="mx-6 mt-3 rounded border border-state-error/30 bg-state-error-light px-4 py-2 text-xs text-state-error">
      {message}
    </div>
  )
}
