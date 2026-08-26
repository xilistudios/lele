import type { ApiErrorResponse, ConfigError } from '../../lib/types'

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code?: string,
    public readonly validationErrors?: ConfigError[],
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

export const parseApiError = async (response: Response): Promise<ApiError> => {
  let payload: ApiErrorResponse | null = null

  try {
    payload = (await response.json()) as ApiErrorResponse
  } catch {
    payload = null
  }

  // Extract validation errors if present (e.g. 422 from config save)
  const validationErrors = Array.isArray(payload?.errors) ? payload.errors : undefined

  // Build a meaningful message: prefer explicit message/error, then first
  // validation error, and finally the HTTP status text as a last resort.
  let message = payload?.message ?? payload?.error
  if (!message && validationErrors && validationErrors.length > 0) {
    message = validationErrors.map((e) => e.message).join('; ')
  }
  message = message ?? response.statusText

  return new ApiError(message, response.status, payload?.code, validationErrors)
}
