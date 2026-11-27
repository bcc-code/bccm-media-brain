<script setup lang="ts">
import { Checkbox } from '@ark-ui/vue/checkbox'

const checked = defineModel<boolean>({ required: true })
defineProps<{ disabled?: boolean }>()
</script>

<template>
  <Checkbox.Root v-model:checked="checked" :disabled="disabled" class="flex cursor-pointer items-center gap-2 data-[disabled]:cursor-not-allowed data-[disabled]:opacity-50">
    <Checkbox.Control
      class="flex size-4 items-center justify-center rounded-[4px] border border-border-strong data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[focus-visible]:focus-ring"
    >
      <Checkbox.Indicator>
        <svg viewBox="0 0 24 24" class="size-3 text-primary-foreground" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M20 6 9 17l-5-5" />
        </svg>
      </Checkbox.Indicator>
    </Checkbox.Control>
    <Checkbox.Label><slot /></Checkbox.Label>
    <Checkbox.HiddenInput />
  </Checkbox.Root>
</template>
