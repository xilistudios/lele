import { describe, expect, test } from 'bun:test'
import { ApiError, parseApiError } from './errors'

describe('ApiError', () => {
  test('crea un error con mensaje, status y code', () => {
    const error = new ApiError('Not found', 404, 'not_found')
    expect(error.message).toBe('Not found')
    expect(error.status).toBe(404)
    expect(error.code).toBe('not_found')
    expect(error.name).toBe('ApiError')
  })

  test('crea un error sin code', () => {
    const error = new ApiError('Server error', 500)
    expect(error.message).toBe('Server error')
    expect(error.status).toBe(500)
    expect(error.code).toBeUndefined()
  })
})

describe('parseApiError', () => {
  test('parsea error con mensaje del body JSON', async () => {
    const response = new Response(JSON.stringify({ code: 'auth_error', message: 'Invalid PIN' }), {
      status: 400,
      headers: { 'Content-Type': 'application/json' },
    })

    const error = await parseApiError(response)
    expect(error.message).toBe('Invalid PIN')
    expect(error.status).toBe(400)
    expect(error.code).toBe('auth_error')
  })

  test('usa statusText si el body no es JSON válido', async () => {
    const response = new Response('not json', {
      status: 500,
      statusText: 'Internal Server Error',
    })

    const error = await parseApiError(response)
    expect(error.message).toBe('Internal Server Error')
    expect(error.status).toBe(500)
  })

  test('usa statusText si el body no se puede parsear', async () => {
    const response = new Response('', {
      status: 503,
      statusText: 'Service Unavailable',
    })

    const error = await parseApiError(response)
    expect(error.message).toBe('Service Unavailable')
    expect(error.status).toBe(503)
  })

  test('maneja response sin message en JSON', async () => {
    const response = new Response(JSON.stringify({ other: 'data' }), {
      status: 401,
      statusText: 'Unauthorized',
      headers: { 'Content-Type': 'application/json' },
    })

    const error = await parseApiError(response)
    expect(error.message).toBe('Unauthorized')
    expect(error.status).toBe(401)
  })

  test('extrae validation errors de un 422', async () => {
    const body = {
      errors: [
        { path: 'agents.defaults.model', message: 'Model is required', code: 'required' },
        { path: 'gateway.port', message: 'Port must be > 0', code: 'invalid' },
      ],
    }
    const response = new Response(JSON.stringify(body), {
      status: 422,
      statusText: 'Unprocessable Entity',
      headers: { 'Content-Type': 'application/json' },
    })

    const error = await parseApiError(response)
    expect(error.status).toBe(422)
    expect(error.validationErrors).toHaveLength(2)
    expect(error.validationErrors![0].path).toBe('agents.defaults.model')
    expect(error.validationErrors![0].message).toBe('Model is required')
    expect(error.validationErrors![1].path).toBe('gateway.port')
    // Message should be the joined validation error messages
    expect(error.message).toBe('Model is required; Port must be > 0')
  })

  test('usa message del body si existe junto con validation errors', async () => {
    const body = {
      message: 'Config validation failed',
      errors: [{ path: 'gateway.port', message: 'Port must be > 0', code: 'invalid' }],
    }
    const response = new Response(JSON.stringify(body), {
      status: 422,
      headers: { 'Content-Type': 'application/json' },
    })

    const error = await parseApiError(response)
    expect(error.message).toBe('Config validation failed')
    expect(error.validationErrors).toHaveLength(1)
  })

  test('crea ApiError con validation errors', () => {
    const errors = [
      { path: 'gateway.port', message: 'Port must be > 0', code: 'invalid' },
    ]
    const error = new ApiError('Validation failed', 422, 'validation_error', errors)
    expect(error.message).toBe('Validation failed')
    expect(error.status).toBe(422)
    expect(error.validationErrors).toEqual(errors)
  })
})
