<script setup lang="ts">
import { Switch } from '@ark-ui/vue/switch'

defineProps<{
  checked: boolean
  disabled?: boolean
  /** What the switch turns on, for screen readers: the column says it for everyone else. */
  label: string
}>()
const emit = defineEmits<{ change: [checked: boolean] }>()
</script>

<template>
  <Switch.Root
    class="inline-flex items-center"
    :checked="checked"
    :disabled="disabled"
    @checked-change="(d) => emit('change', d.checked)"
  >
    <Switch.Control
      class="inline-flex h-5 w-9 cursor-pointer items-center rounded-full bg-border-strong p-0.5 transition-colors data-[state=checked]:bg-primary data-[disabled]:cursor-default data-[disabled]:opacity-50 data-[focus-visible]:focus-ring"
    >
      <Switch.Thumb
        class="size-4 rounded-full bg-foreground transition-transform data-[state=checked]:translate-x-4 data-[state=checked]:bg-primary-foreground"
      />
    </Switch.Control>
    <Switch.Label class="sr-only">{{ label }}</Switch.Label>
    <Switch.HiddenInput />
  </Switch.Root>
</template>
