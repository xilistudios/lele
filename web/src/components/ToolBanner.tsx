import type { ToolStatus } from '../lib/types'

export function ToolBanner({ tool }: { tool: ToolStatus }) {
  return (
    <div className="rounded-2xl border border-sky-900 bg-sky-950/50 px-4 py-3 text-sm text-sky-100">
      <span className="font-medium">{tool.tool}</span>
      <span className="mx-2 text-sky-300">·</span>
      <span>{tool.action}</span>
    </div>
  )
}
