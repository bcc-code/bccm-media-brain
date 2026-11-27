import { ref } from 'vue'
import { gql, type ApiError, type Fetcher } from '../utils/graphql'
import { usePaged, type Page } from './usePaged'

export interface Person {
  id: string
  email: string | null
  name: string | null
  active: boolean
  isAdmin: boolean
  createdAt: string
  groups: { id: string; name: string }[]
}

export const USER_PAGE = 50

const FIELDS = 'id email name active isAdmin createdAt groups { id name }'

/** People (not service accounts), searched by name or email. */
export function useUsers(fetcher?: Fetcher) {
  const search = ref('')
  /** The caller, whose own row cannot be switched off or demoted from here. */
  const meId = ref<string | null>(null)
  /** Ids of the users with a change waiting for the server. */
  const saving = ref<Set<string>>(new Set())
  const changeError = ref<ApiError | null>(null)

  const list = usePaged<Person>(async (after) => {
    const text = search.value.trim()
    const data = await gql<{ me: { id: string }; users: Page<Person> }>(
      `query Users($filter: UserFilter, $first: Int, $after: String) {
        me { id }
        users(filter: $filter, first: $first, after: $after) {
          nodes { ${FIELDS} }
          pageInfo { hasNextPage endCursor }
        }
      }`,
      { filter: { kind: 'PERSON', search: text || null }, first: USER_PAGE, after },
      fetcher,
    )
    meId.value = data.me.id
    return data.users
  })

  /** Sends a mutation that answers the changed user and puts the answer in the list. */
  async function change(id: string, mutation: string, variables: Record<string, unknown>) {
    changeError.value = null
    saving.value = new Set(saving.value).add(id)
    try {
      const data = await gql<Record<string, Person>>(mutation, variables, fetcher)
      const updated = Object.values(data)[0]!
      list.items.value = list.items.value.map((u) => (u.id === updated.id ? updated : u))
    } catch (e) {
      changeError.value = e as ApiError
    } finally {
      const next = new Set(saving.value)
      next.delete(id)
      saving.value = next
    }
  }

  function setAdmin(id: string, admin: boolean) {
    return change(
      id,
      `mutation SetUserAdmin($user: String!, $admin: Boolean!) {
        setUserAdmin(user: $user, admin: $admin) { ${FIELDS} }
      }`,
      { user: id, admin },
    )
  }

  function setActive(id: string, active: boolean) {
    return change(
      id,
      `mutation SetUserActive($user: String!, $active: Boolean!) {
        setUserActive(user: $user, active: $active) { ${FIELDS} }
      }`,
      { user: id, active },
    )
  }

  return { users: list.items, search, meId, saving, changeError, setAdmin, setActive, ...list }
}
