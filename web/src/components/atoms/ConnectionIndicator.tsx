type Props = {
  status: 'disconnected' | 'connecting' | 'connected'
}

export function ConnectionIndicator({ status }: Props) {
  const className =
    status === 'connected'
      ? 'bg-emerald-400'
      : status === 'connecting'
        ? 'bg-yellow-400'
        : 'bg-[#555]'

  return <span className={`h-1.5 w-1.5 rounded-full ${className}`} />
}
