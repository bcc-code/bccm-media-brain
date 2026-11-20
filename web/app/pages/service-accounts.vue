<script setup lang="ts">
import { onMounted } from 'vue'
import AccountSwitch from '../components/AccountSwitch.vue'
import CreateServiceAccountForm from '../components/CreateServiceAccountForm.vue'
import { awaitsFirstSignIn, useServiceAccounts } from '../composables/useServiceAccounts'

const { accounts, loading, loaded, error, saving, load, create, setActive, setAdmin } = useServiceAccounts()

onMounted(load)

const th = 'px-4 py-2.5 text-left text-xs font-medium uppercase tracking-wider text-muted-foreground'
const td = 'border-t border-border px-4 py-3 align-middle'
const badge = 'inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-xs font-medium'
</script>

<template>
  <main class="mx-auto flex max-w-[1200px] flex-col gap-6 p-8">
    <header class="flex flex-col gap-1.5">
      <h1 class="text-3xl font-semibold leading-tight tracking-tight">Service accounts</h1>
      <p class="text-[15px] text-muted-foreground">
        Machine clients. Credentials are made, rotated and withdrawn in authentik.
      </p>
    </header>

    <div
      v-if="error"
      role="alert"
      class="flex flex-wrap items-center justify-between gap-3 rounded-control border border-destructive/40 bg-destructive/10 px-4 py-3"
    >
      <span>{{ error.message }}</span>
      <button
        type="button"
        class="h-8 rounded-control border border-border-strong px-3 font-medium hover:bg-accent focus-visible:focus-ring"
        @click="load"
      >
        Reload
      </button>
    </div>

    <div class="flex flex-wrap items-start gap-6">
      <section class="flex min-w-0 flex-[999_1_560px] flex-col gap-3" aria-label="Service accounts">
        <div class="overflow-hidden rounded-card border border-border bg-card">
          <div class="overflow-x-auto">
            <table class="w-full min-w-[600px] border-collapse">
              <thead>
                <tr>
                  <th :class="th">Account</th>
                  <th :class="th">Sign-in</th>
                  <th :class="th">Role</th>
                  <th :class="th">Created</th>
                  <th :class="th">Active</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="a in accounts" :key="a.id" data-test="account">
                  <td :class="td">
                    <div class="flex flex-col font-mono">
                      <span class="font-medium">{{ a.name ?? a.externalId }}</span>
                      <span class="text-xs text-muted-foreground">{{ a.id }}</span>
                    </div>
                  </td>
                  <td :class="td">
                    <span v-if="awaitsFirstSignIn(a)" :class="[badge, 'border-warning/30 bg-warning/15 text-warning']">
                      <span class="size-1.5 rounded-full bg-current" aria-hidden="true" />Waiting for first sign-in
                    </span>
                    <span v-else :class="[badge, 'border-success/30 bg-success/15 text-success']">
                      <span class="size-1.5 rounded-full bg-current" aria-hidden="true" />Signed in
                    </span>
                  </td>
                  <td :class="td">
                    <div class="flex items-center gap-2.5">
                      <AccountSwitch
                        data-test="admin"
                        :checked="a.isAdmin"
                        :disabled="saving.has(a.id)"
                        :label="`${a.name ?? a.id} administrator`"
                        @change="(on) => setAdmin(a.id, on)"
                      />
                      <span :class="a.isAdmin ? 'font-medium' : 'text-muted-foreground'">
                        {{ a.isAdmin ? 'Administrator' : 'Grants only' }}
                      </span>
                    </div>
                  </td>
                  <td :class="[td, 'font-mono text-[13px]']">{{ a.createdAt.slice(0, 10) }}</td>
                  <td :class="td">
                    <AccountSwitch
                      data-test="active"
                      :checked="a.active"
                      :disabled="saving.has(a.id)"
                      :label="`${a.name ?? a.id} active`"
                      @change="(on) => setActive(a.id, on)"
                    />
                  </td>
                </tr>
                <tr v-if="loaded && accounts.length === 0">
                  <td :class="[td, 'text-muted-foreground']" colspan="5">No service accounts yet.</td>
                </tr>
                <tr v-if="loading && !loaded">
                  <td :class="[td, 'text-muted-foreground']" colspan="5">Loading…</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
        <p class="text-xs text-muted-foreground">
          A change takes up to a minute to apply. An account that is not an administrator can do nothing until it has grants.
        </p>
      </section>

      <aside class="min-w-0 max-w-[380px] flex-[1_1_300px]">
        <CreateServiceAccountForm :create="create" />
      </aside>
    </div>
  </main>
</template>
