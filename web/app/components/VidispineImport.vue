<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useVidispineImport } from '../composables/useVidispineImport'

const emit = defineEmits<{ changed: [] }>()
const { isAdmin, types, projects, lookup, busy, error, done, prepare, check, importItem, reimport } = useVidispineImport()

const itemId = ref('')
const typeId = ref<number | null>(null)
const projectId = ref('')

onMounted(prepare)
watch(types, (t) => {
  if (typeId.value === null && t.length > 0) typeId.value = t[0]!.id
})
watch(done, (d) => {
  if (!d) return
  itemId.value = ''
  emit('changed')
})

const input =
  'h-9 rounded-control border border-border bg-background px-3 text-foreground placeholder:text-muted-foreground/60 focus-visible:focus-ring'
const button =
  'h-9 rounded-control bg-primary px-4 font-medium text-primary-foreground hover:bg-primary/90 focus-visible:focus-ring disabled:pointer-events-none disabled:opacity-50'
</script>

<template>
  <section v-if="isAdmin" class="flex flex-col gap-4 rounded-card border border-border bg-card p-6" aria-label="Import from Vidispine">
    <div class="flex flex-col gap-1">
      <h2 class="text-lg font-medium leading-snug">Import from Vidispine</h2>
      <p class="text-[13px] text-muted-foreground">
        The original, the watermarked preview and Mediabanken's transcript. An item imported before is re-imported:
        its Vidispine fields are refreshed and what is new is added.
      </p>
    </div>

    <form class="flex flex-wrap items-center gap-3" novalidate @submit.prevent="check(itemId)">
      <input
        v-model="itemId"
        aria-label="Vidispine item id"
        placeholder="VX-123"
        :class="[input, 'w-[180px] font-mono']"
        autocomplete="off"
        spellcheck="false"
      />
      <button type="submit" :class="button" :disabled="busy || !itemId.trim()">Find</button>
    </form>

    <div v-if="lookup?.asset" class="flex flex-wrap items-center gap-3" data-test="existing">
      <span>
        <span class="font-mono">{{ lookup.itemId }}</span> is
        <span class="font-medium">{{ lookup.asset.title }}</span>
        <span class="font-mono text-xs text-muted-foreground"> {{ lookup.asset.id }}</span>
      </span>
      <button type="button" data-test="reimport" :class="button" :disabled="busy" @click="reimport">
        {{ busy ? 'Re-importing…' : 'Re-import' }}
      </button>
    </div>

    <form
      v-else-if="lookup"
      class="flex flex-wrap items-end gap-3"
      data-test="new"
      novalidate
      @submit.prevent="typeId !== null && importItem(typeId, projectId || null)"
    >
      <label class="flex flex-col gap-1.5">
        <span class="font-medium">Type</span>
        <select v-model="typeId" data-test="type" :class="input">
          <option v-for="t in types" :key="t.id" :value="t.id">{{ t.name }}</option>
        </select>
      </label>
      <label class="flex flex-col gap-1.5">
        <span class="font-medium">Project</span>
        <select v-model="projectId" data-test="project" :class="input">
          <option value="">None (administrators only)</option>
          <option v-for="p in projects" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
      </label>
      <button type="submit" data-test="import" :class="button" :disabled="busy || typeId === null">
        {{ busy ? 'Importing…' : `Import ${lookup.itemId}` }}
      </button>
    </form>

    <p v-if="error" role="alert" class="text-[13px] text-destructive">{{ error.message }}</p>
    <p v-if="done" role="status" class="text-[13px]" data-test="done">
      {{ done.verb }} <span class="font-medium">{{ done.asset.title }}</span>
      <span class="font-mono text-xs text-muted-foreground"> {{ done.asset.id }}</span>
    </p>
  </section>
</template>
