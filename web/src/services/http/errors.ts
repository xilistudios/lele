import type { ApiErrorResponse } from '../../lib/types'

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code?: string,
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

  return new ApiError(payload?.message ?? response.statusText, response.status, payload?.code)
}
