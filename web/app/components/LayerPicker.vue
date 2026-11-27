<script setup lang="ts">
import { ref, watch } from 'vue'
import CheckOption from './CheckOption.vue'
import { LAYER_COLORS, MAX_LAYERS } from '../composables/useAsset'
import { trackLabel, type Track } from '../composables/useSubtitles'

const props = defineProps<{
  tracks: Track[]
  /** Revision ids in layer order. */
  selected: string[]
}>()
const emit = defineEmits<{ toggle: [id: string] }>()
const highlight = defineModel<boolean>('highlight', { required: true })
const showTags = defineModel<boolean>('showTags', { required: true })

/** Open tracks by rendition id. A track with a revision on the preview opens by itself. */
const open = ref<Record<string, boolean>>({})
watch(
  () => [props.tracks, props.selected] as const,
  ([tracks, selected]) => {
    for (const t of tracks) if (t.revisions.some((r) => selected.includes(r.id))) open.value[t.renditionId] ??= true
  },
  { immediate: true },
)

function shownOf(t: Track): number {
  return t.revisions.filter((r) => props.selected.includes(r.id)).length
}

function summary(t: Track): string {
  const n = t.revisions.length
  const shown = shownOf(t)
  return [n === 1 ? '1 revision' : `${n} revisions`, shown ? `${shown} shown` : ''].filter(Boolean).join(' · ')
}
</script>

<template>
  <section class="flex flex-col gap-4 rounded-card border border-border bg-card px-6 py-5" aria-labelledby="layers-title">
    <div class="flex flex-wrap items-baseline justify-between gap-2">
      <div class="flex flex-col gap-0.5">
        <h2 id="layers-title" class="text-lg font-medium leading-snug">Subtitles on the preview</h2>
        <p class="text-[13px] text-muted-foreground">
          Show up to {{ MAX_LAYERS }} revisions from any track. Pick several revisions of one track to compare models.
        </p>
      </div>
      <span class="font-mono text-xs text-muted-foreground" data-test="count">{{ selected.length }} of {{ MAX_LAYERS }} shown</span>
    </div>

    <p v-if="tracks.length === 0" data-test="no-tracks" class="text-muted-foreground">This asset has no subtitles.</p>

    <template v-else>
      <div class="flex flex-wrap gap-x-6 gap-y-2">
        <CheckOption v-model="highlight" data-test="highlight">Highlight words that differ from layer 1</CheckOption>
        <CheckOption v-model="showTags" data-test="show-tags">Label lines with their revision</CheckOption>
      </div>

      <ul class="flex flex-col overflow-hidden rounded-control border border-border">
        <li v-for="t in tracks" :key="t.renditionId" data-test="track" class="border-b border-border last:border-b-0">
          <button
            type="button"
            data-test="track-toggle"
            class="flex min-h-12 w-full items-center gap-2.5 bg-card px-3.5 py-2.5 text-left hover:bg-raised focus-visible:focus-ring"
            :aria-expanded="!!open[t.renditionId]"
            @click="open[t.renditionId] = !open[t.renditionId]"
          >
            <svg
              aria-hidden="true"
              class="size-4 shrink-0 transition-transform"
              :class="{ 'rotate-90': open[t.renditionId] }"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <path d="m9 18 6-6-6-6" />
            </svg>
            <span class="font-medium">{{ trackLabel(t) }}</span>
            <span class="flex-1" />
            <span class="text-xs text-muted-foreground">{{ summary(t) }}</span>
          </button>
          <ul v-if="open[t.renditionId]" class="flex flex-col bg-background">
            <li
              v-for="(r, i) in t.revisions"
              :key="r.id"
              data-test="revision"
              class="flex flex-wrap items-center gap-3 border-t border-border py-2.5 pl-10 pr-3.5"
            >
              <CheckOption
                class="w-14 shrink-0"
                :model-value="selected.includes(r.id)"
                :disabled="!selected.includes(r.id) && selected.length >= MAX_LAYERS"
                @update:model-value="emit('toggle', r.id)"
              >
                <span class="font-mono">r{{ r.revision }}</span>
              </CheckOption>
              <span class="flex min-w-0 flex-[1_1_240px] flex-wrap items-center gap-2 text-xs">
                <span class="rounded-[6px] border border-border-strong px-1.5 py-0.5 font-medium">{{ r.origin }}</span>
                <span v-if="r.source" class="font-mono">{{ r.source }}</span>
                <span class="font-mono text-muted-foreground">{{ r.createdAt.slice(0, 10) }}</span>
                <span v-if="i === 0" class="rounded-[6px] bg-accent px-1.5 py-0.5 font-medium">Latest</span>
              </span>
              <span class="w-[72px] text-right text-xs">
                <span v-if="selected.includes(r.id)" data-test="slot" class="inline-flex items-center gap-1.5">
                  <span
                    aria-hidden="true"
                    class="size-3 rounded-[3px] border border-[#737373]"
                    :style="{ background: LAYER_COLORS[selected.indexOf(r.id)] }"
                  />
                  Layer {{ selected.indexOf(r.id) + 1 }}
                </span>
              </span>
            </li>
          </ul>
        </li>
      </ul>
      <p v-if="selected.length >= MAX_LAYERS" class="text-xs text-muted-foreground">
        {{ MAX_LAYERS }} layers shown. Untick one to add another revision.
      </p>
    </template>
  </section>
</template>
