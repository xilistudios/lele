import { Component, type ErrorInfo, type ReactNode } from 'react'

type Props = { children: ReactNode }
type State = { error: Error | null }

/** Catches render crashes so the app does not blank out with no explanation. */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('UI crash:', error, info.componentStack)
  }

  render() {
    if (!this.state.error) return this.props.children

    return (
      <main className="flex min-h-screen items-center justify-center bg-background-primary px-4 py-12">
        <div className="w-full max-w-md space-y-4 rounded-xl border border-border bg-background-secondary p-6 text-center shadow-xl">
          <h1 className="text-lg font-semibold text-text-primary">Something went wrong</h1>
          <p className="text-sm text-text-secondary break-words">{this.state.error.message}</p>
          <button
            type="button"
            onClick={() => {
              this.setState({ error: null })
              window.location.reload()
            }}
            className="rounded-md bg-accent-primary px-4 py-2 text-sm font-medium text-text-on-accent hover:bg-accent-hover"
          >
            Reload
          </button>
        </div>
      </main>
    )
  }
}
