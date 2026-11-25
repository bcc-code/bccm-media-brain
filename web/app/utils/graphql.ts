/** An error answer of the API. `code` is `extensions.code`, or `HTTP_<status>` when the request was refused before GraphQL (sign-in). */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly code: string,
    /** The input field an `INVALID_INPUT` is about. */
    readonly field?: string,
    /** The kind of a `CONFLICT`, such as `ALREADY_EXISTS`. */
    readonly reason?: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

interface GraphqlError {
  message: string
  extensions?: { code?: string; field?: string; reason?: string }
}

export type Fetcher = typeof fetch

export async function gql<T>(
  query: string,
  variables: Record<string, unknown> = {},
  fetcher: Fetcher = fetch,
): Promise<T> {
  let res: Response
  try {
    res = await fetcher('/query', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ query, variables }),
    })
  } catch {
    throw new ApiError('The server cannot be reached.', 'NETWORK')
  }
  if (res.status === 401) reloadForSignIn()

  let body: { data?: T; errors?: GraphqlError[] } | undefined
  try {
    body = await res.json()
  } catch {
    // A refused sign-in answers in plain text, not GraphQL.
    body = undefined
  }

  const first = body?.errors?.[0]
  if (first) {
    const ext = first.extensions ?? {}
    throw new ApiError(first.message, ext.code ?? 'INTERNAL', ext.field, ext.reason)
  }
  if (!res.ok || !body?.data) {
    throw new ApiError(httpMessage(res.status), `HTTP_${res.status}`)
  }
  return body.data
}

const RELOADED_AT = 'mb-reloaded-for-sign-in'
const RELOAD_PAUSE_MS = 30_000

export interface ReloadDeps {
  storage?: Pick<Storage, 'getItem' | 'setItem'>
  reload?: () => void
  now?: () => number
}

/**
 * Reloads the page so the proxy can send the browser through sign-in again: a 401 means the
 * session behind the page has expired. Only once per RELOAD_PAUSE_MS, so a 401 that a reload
 * cannot cure (a proxy that never signs in) shows its error instead of reloading for ever.
 * Without session storage there is no way to tell, so it does not reload.
 */
export function reloadForSignIn(deps: ReloadDeps = {}): boolean {
  const now = (deps.now ?? Date.now)()
  try {
    const storage = deps.storage ?? sessionStorage
    const last = Number(storage.getItem(RELOADED_AT))
    if (last && now - last < RELOAD_PAUSE_MS) return false
    storage.setItem(RELOADED_AT, String(now))
  } catch {
    return false
  }
  ;(deps.reload ?? (() => location.reload()))()
  return true
}

function httpMessage(status: number): string {
  if (status === 401 || status === 403) return 'Not signed in, or not allowed to use MediaBrain.'
  return `The server answered ${status}.`
}
