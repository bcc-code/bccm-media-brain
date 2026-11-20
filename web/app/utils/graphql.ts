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

function httpMessage(status: number): string {
  if (status === 401 || status === 403) return 'Not signed in, or not allowed to use MediaBrain.'
  return `The server answered ${status}.`
}
