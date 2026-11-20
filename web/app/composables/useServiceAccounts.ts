import { ref } from 'vue'
import { ApiError, gql, type Fetcher } from '../utils/graphql'

export interface ServiceAccount {
  id: string
  name: string | null
  externalId: string
  active: boolean
  isAdmin: boolean
  createdAt: string
}

export interface NewServiceAccount {
  name: string
  admin: boolean
  /** Id of a user that already signed in as a person and becomes this account. */
  user?: string
}

const FIELDS = 'id name externalId active isAdmin createdAt'

/**
 * Until its first sign-in the server stores an account under `service|<name>`; after it, under
 * the subject of the token that took it (docs/security.md, "Machine clients").
 */
export function awaitsFirstSignIn(a: ServiceAccount): boolean {
  return a.externalId.startsWith('service|')
}

export function useServiceAccounts(fetcher?: Fetcher) {
  const accounts = ref<ServiceAccount[]>([])
  const loading = ref(false)
  const loaded = ref(false)
  const error = ref<ApiError | null>(null)
  /** Ids of the accounts with a change waiting for the server. */
  const saving = ref<Set<string>>(new Set())

  async function load() {
    loading.value = true
    error.value = null
    try {
      const data = await gql<{ serviceAccounts: ServiceAccount[] }>(
        `query ServiceAccounts { serviceAccounts { ${FIELDS} } }`,
        {},
        fetcher,
      )
      accounts.value = data.serviceAccounts
      loaded.value = true
    } catch (e) {
      error.value = e as ApiError
    } finally {
      loading.value = false
    }
  }

  /** Throws the ApiError, so the form can show it beside the field it is about. */
  async function create(input: NewServiceAccount): Promise<ServiceAccount> {
    const data = await gql<{ createServiceAccount: ServiceAccount }>(
      `mutation CreateServiceAccount($input: ServiceAccountInput!) {
        createServiceAccount(input: $input) { ${FIELDS} }
      }`,
      { input: { name: input.name, admin: input.admin, user: input.user || null } },
      fetcher,
    )
    const made = data.createServiceAccount
    // A converted user keeps its id; it was not in this list before.
    accounts.value = [...accounts.value.filter((a) => a.id !== made.id), made]
    return made
  }

  /** Sends a mutation that answers the changed account and puts the answer in the list. */
  async function change(id: string, mutation: string, variables: Record<string, unknown>) {
    error.value = null
    saving.value = new Set(saving.value).add(id)
    try {
      const data = await gql<Record<string, ServiceAccount>>(mutation, variables, fetcher)
      const updated = Object.values(data)[0]!
      accounts.value = accounts.value.map((a) => (a.id === updated.id ? updated : a))
    } catch (e) {
      error.value = e as ApiError
    } finally {
      const next = new Set(saving.value)
      next.delete(id)
      saving.value = next
    }
  }

  function setActive(id: string, active: boolean) {
    return change(
      id,
      `mutation SetServiceAccountActive($serviceAccount: String!, $active: Boolean!) {
        setServiceAccountActive(serviceAccount: $serviceAccount, active: $active) { ${FIELDS} }
      }`,
      { serviceAccount: id, active },
    )
  }

  /** setUserAdmin is the one operation for people and service accounts alike. */
  function setAdmin(id: string, admin: boolean) {
    return change(
      id,
      `mutation SetUserAdmin($user: String!, $admin: Boolean!) {
        setUserAdmin(user: $user, admin: $admin) { ${FIELDS} }
      }`,
      { user: id, admin },
    )
  }

  return { accounts, loading, loaded, error, saving, load, create, setActive, setAdmin }
}
