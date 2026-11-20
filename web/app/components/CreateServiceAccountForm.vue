<script setup lang="ts">
import { computed, ref } from 'vue'
import { Checkbox } from '@ark-ui/vue/checkbox'
import { Field } from '@ark-ui/vue/field'
import type { NewServiceAccount } from '../composables/useServiceAccounts'
import { ApiError } from '../utils/graphql'

const props = defineProps<{
  /** Rejects with an ApiError when the server refuses. */
  create: (input: NewServiceAccount) => Promise<unknown>
}>()

const name = ref('')
const admin = ref(false)
const user = ref('')
const busy = ref(false)
const failure = ref<ApiError | null>(null)

// A name that is taken comes back as a conflict without a field.
const failedField = computed(() => {
  const e = failure.value
  if (!e) return null
  if (e.field === 'name' || e.field === 'user') return e.field
  return e.reason === 'ALREADY_EXISTS' ? 'name' : null
})

async function submit() {
  if (busy.value || !name.value.trim()) return
  busy.value = true
  failure.value = null
  try {
    await props.create({ name: name.value.trim(), admin: admin.value, user: user.value.trim() || undefined })
    name.value = ''
    admin.value = false
    user.value = ''
  } catch (e) {
    failure.value = e instanceof ApiError ? e : new ApiError('Something went wrong.', 'INTERNAL')
  } finally {
    busy.value = false
  }
}

const input =
  'h-9 w-full rounded-control border border-border bg-background px-3 text-foreground placeholder:text-muted-foreground/60 focus-visible:focus-ring data-[invalid]:border-destructive'
</script>

<template>
  <form class="flex flex-col gap-4 rounded-card border border-border bg-card p-6" novalidate @submit.prevent="submit">
    <h2 class="text-lg font-medium leading-snug">Create service account</h2>

    <Field.Root class="flex flex-col gap-1.5" required :invalid="failedField === 'name'">
      <Field.Label class="font-medium">Username</Field.Label>
      <Field.Input v-model="name" :class="[input, 'font-mono']" placeholder="ingest" autocomplete="off" spellcheck="false" />
      <Field.HelperText class="text-xs text-muted-foreground">
        As at the identity provider. The first sign-in is matched on it.
      </Field.HelperText>
      <Field.ErrorText class="text-xs text-destructive">{{ failure?.message }}</Field.ErrorText>
    </Field.Root>

    <div class="flex flex-col gap-1">
      <Checkbox.Root v-model:checked="admin" class="flex cursor-pointer items-center gap-2.5">
        <Checkbox.Control
          class="flex size-4 items-center justify-center rounded-[4px] border border-border-strong data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[focus-visible]:focus-ring"
        >
          <Checkbox.Indicator>
            <svg viewBox="0 0 24 24" class="size-3 text-primary-foreground" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M20 6 9 17l-5-5" />
            </svg>
          </Checkbox.Indicator>
        </Checkbox.Control>
        <Checkbox.Label class="font-medium">System administrator</Checkbox.Label>
        <Checkbox.HiddenInput />
      </Checkbox.Root>
      <p class="pl-[26px] text-xs text-muted-foreground">Needed to register files. Can be changed in the list.</p>
    </div>

    <Field.Root class="flex flex-col gap-1.5" :invalid="failedField === 'user'">
      <Field.Label class="font-medium">Convert an existing user</Field.Label>
      <Field.Input v-model="user" :class="[input, 'font-mono']" placeholder="US-…" autocomplete="off" spellcheck="false" />
      <Field.HelperText class="text-xs text-muted-foreground">
        Optional. The id of an account that already signed in as a person. There is no way back.
      </Field.HelperText>
      <Field.ErrorText class="text-xs text-destructive">{{ failure?.message }}</Field.ErrorText>
    </Field.Root>

    <p v-if="failure && !failedField" role="alert" class="text-xs text-destructive">{{ failure.message }}</p>

    <button
      type="submit"
      class="h-9 rounded-control bg-primary px-4 font-medium text-primary-foreground hover:bg-primary/90 focus-visible:focus-ring disabled:pointer-events-none disabled:opacity-50"
      :disabled="busy || !name.trim()"
    >
      {{ busy ? 'Creating…' : 'Create account' }}
    </button>
  </form>
</template>
