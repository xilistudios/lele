import type { ReactNode } from 'react'

type Step = {
  id: number
  title: string
  icon: ReactNode
}

type Props = {
  steps: Step[]
  currentStep: number
  onStepClick?: (step: number) => void
}

export function StepIndicator({ steps, currentStep, onStepClick }: Props) {
  return (
    <div className="relative">
      {/* Progress line background */}
      <div className="absolute top-5 left-0 right-0 h-0.5 bg-border" />

      {/* Progress line fill */}
      <div
        className="absolute top-5 left-0 h-0.5 bg-gradient-to-r from-blue-500 to-blue-400 transition-all duration-300"
        style={{ width: `${((currentStep - 1) / (steps.length - 1)) * 100}%` }}
      />

      <div className="relative flex justify-between">
        {steps.map((step) => {
          const isActive = step.id === currentStep
          const isCompleted = step.id < currentStep
          const isClickable = onStepClick && (isCompleted || step.id === currentStep + 1)

          return (
            <div key={step.id} className="flex flex-col items-center gap-2">
              <button
                type="button"
                onClick={() => isClickable && onStepClick(step.id)}
                disabled={!isClickable}
                className={`
                  relative z-10 flex h-10 w-10 items-center justify-center rounded-full
                  transition-all duration-300 shadow-sm
                  ${
                    isActive
                      ? 'bg-blue-500 text-white shadow-blue-500/25 scale-110'
                      : isCompleted
                        ? 'bg-blue-500/20 text-blue-400 border-2 border-blue-500 hover:bg-blue-500/30'
                        : 'bg-background-secondary text-text-tertiary border-2 border-border'
                  }
                  ${isClickable ? 'cursor-pointer' : 'cursor-default'}
                `}
              >
                {isCompleted ? (
                  <svg
                    width="18"
                    height="18"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2.5"
                  >
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                ) : (
                  step.icon
                )}
              </button>
              <span
                className={`
                  text-xs font-medium transition-colors duration-200
                  ${isActive ? 'text-blue-400' : isCompleted ? 'text-text-secondary' : 'text-text-tertiary'}
                `}
              >
                {step.title}
              </span>
            </div>
          )
        })}
      </div>
    </div>
  )
}
