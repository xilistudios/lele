type Props = {
  message: string
}

export function ErrorBanner({ message }: Props) {
  return (
    <div className="mx-6 mt-3 rounded border border-red-900/50 bg-red-950/30 px-4 py-2 text-xs text-red-300">
      {message}
    </div>
  )
}
