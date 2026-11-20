import { vi } from 'vitest'
import type { ServiceAccount } from '../app/composables/useServiceAccounts'

export function account(over: Partial<ServiceAccount> = {}): ServiceAccount {
  return {
    id: 'US-1',
    name: 'ingest',
    externalId: 'service|ingest',
    active: true,
    isAdmin: false,
    createdAt: '2026-11-11T09:30:00Z',
    ...over,
  }
}

export function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

export function apiError(message: string, extensions: Record<string, string>): Response {
  return json({ errors: [{ message, extensions }], data: null })
}

/** A fetch that answers the given responses in order and records the request bodies. */
export function fakeFetch(...answers: (Response | Error)[]) {
  const sent: { query: string; variables: Record<string, unknown> }[] = []
  const fetcher = vi.fn(async (_url: unknown, init?: RequestInit) => {
    sent.push(JSON.parse(String(init?.body)))
    const next = answers.shift()
    if (!next) throw new Error('unexpected request')
    if (next instanceof Error) throw next
    return next
  })
  return { fetcher: fetcher as unknown as typeof fetch, sent }
}
