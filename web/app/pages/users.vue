<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'
import AccountSwitch from '../components/AccountSwitch.vue'
import { useUsers } from '../composables/useUsers'

const { users, search, meId, loading, loaded, error, changeError, hasMore, saving, reload, more, setAdmin, setActive } =
  useUsers()

onMounted(reload)

const SEARCH_DELAY_MS = 300
let timer: ReturnType<typeof setTimeout> | undefined
watch(search, () => {
  clearTimeout(timer)
  timer = setTimeout(reload, SEARCH_DELAY_MS)
})
onBeforeUnmount(() => clearTimeout(timer))

const shownError = computed(() => error.value ?? changeError.value)

const th = 'px-4 py-2.5 text-left text-xs font-medium uppercase tracking-wider text-muted-foreground'
const td = 'border-t border-border px-4 py-3 align-middle'
const badge = 'inline-flex items-center rounded-md border border-border px-2 py-0.5 text-xs font-medium'
const input =
  'h-9 rounded-control border border-border bg-background px-3 text-foreground placeholder:text-muted-foreground/60 focus-visible:focus-ring'
</script>

<template>
  <main class="mx-auto flex max-w-[1200px] flex-col gap-6 p-8">
    <header class="flex flex-col gap-1.5">
      <h1 class="text-3xl font-semibold leading-tight tracking-tight">Users</h1>
      <p class="text-[15px] text-muted-foreground">
        People who have signed in. Groups come from the identity provider.
      </p>
    </header>

    <input
      v-model="search"
      type="search"
      aria-label="Search by name or email"
      placeholder="Search by name or email"
      :class="[input, 'max-w-[420px]']"
      autocomplete="off"
      spellcheck="false"
    />

    <div
      v-if="shownError"
      role="alert"
      class="flex flex-wrap items-center justify-between gap-3 rounded-control border border-destructive/40 bg-destructive/10 px-4 py-3"
    >
      <span>{{ shownError.message }}</span>
      <button
        type="button"
        class="h-8 rounded-control border border-border-strong px-3 font-medium hover:bg-accent focus-visible:focus-ring"
        @click="reload"
      >
        Reload
      </button>
    </div>

    <section class="flex flex-col gap-3" aria-label="Users">
      <div class="overflow-hidden rounded-card border border-border bg-card">
        <div class="overflow-x-auto">
          <table class="w-full min-w-[720px] border-collapse">
            <thead>
              <tr>
                <th :class="th">User</th>
                <th :class="th">Groups</th>
                <th :class="th">Role</th>
                <th :class="th">Created</th>
                <th :class="th">Active</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="u in users" :key="u.id" data-test="user">
                <td :class="td">
                  <div class="flex flex-col">
                    <span class="font-medium">
                      {{ u.name ?? u.email ?? u.id }}
                      <span v-if="u.id === meId" class="text-xs font-normal text-muted-foreground">(you)</span>
                    </span>
                    <span v-if="u.email && u.name" class="text-[13px] text-muted-foreground">{{ u.email }}</span>
                    <span class="font-mono text-xs text-muted-foreground">{{ u.id }}</span>
                  </div>
                </td>
                <td :class="td">
                  <div class="flex flex-wrap gap-1.5">
                    <span v-for="g in u.groups" :key="g.id" :class="badge" data-test="group">{{ g.name }}</span>
                    <span v-if="u.groups.length === 0" class="text-muted-foreground">—</span>
                  </div>
                </td>
                <td :class="td">
                  <div class="flex items-center gap-2.5">
                    <AccountSwitch
                      data-test="admin"
                      :checked="u.isAdmin"
                      :disabled="saving.has(u.id) || u.id === meId"
                      :label="`${u.name ?? u.id} administrator`"
                      @change="(on) => setAdmin(u.id, on)"
                    />
                    <span :class="u.isAdmin ? 'font-medium' : 'text-muted-foreground'">
                      {{ u.isAdmin ? 'Administrator' : 'Grants only' }}
                    </span>
                  </div>
                </td>
                <td :class="[td, 'font-mono text-[13px]']">{{ u.createdAt.slice(0, 10) }}</td>
                <td :class="td">
                  <AccountSwitch
                    data-test="active"
                    :checked="u.active"
                    :disabled="saving.has(u.id) || u.id === meId"
                    :label="`${u.name ?? u.id} active`"
                    @change="(on) => setActive(u.id, on)"
                  />
                </td>
              </tr>
              <tr v-if="loaded && users.length === 0">
                <td :class="[td, 'text-muted-foreground']" colspan="5">
                  {{ search.trim() ? 'Nobody matches.' : 'Nobody has signed in yet.' }}
                </td>
              </tr>
              <tr v-if="loading && !loaded">
                <td :class="[td, 'text-muted-foreground']" colspan="5">Loading…</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
      <button
        v-if="hasMore"
        type="button"
        data-test="more"
        class="h-9 self-start rounded-control border border-border px-4 font-medium hover:bg-accent focus-visible:focus-ring disabled:pointer-events-none disabled:opacity-50"
        :disabled="loading"
        @click="more"
      >
        {{ loading ? 'Loading…' : 'Load more' }}
      </button>
      <p class="text-xs text-muted-foreground">
        A change takes up to a minute to apply. A user who is switched off is refused. You cannot change your own account here.
      </p>
    </section>
  </main>
</template>
