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
})
