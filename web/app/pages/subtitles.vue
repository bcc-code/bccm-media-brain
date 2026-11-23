<script setup lang="ts">
import { computed, ref } from 'vue'
import { Field } from '@ark-ui/vue/field'
import CheckOption from '../components/CheckOption.vue'
import { timecode, trackLabel, useSubtitles, type Cue, type Revision } from '../composables/useSubtitles'

const {
  asset,
  candidates,
  notFound,
  tracks,
  loading,
  error,
  track,
  shown,
  base,
  shownCues,
  cuesLoading,
  ignoreTiming,
  comparison,
  open,
  choose,
  selectTrack,
  show,
  compare,
} = useSubtitles()

const wanted = ref('')
const changesOnly = ref(false)

/** One table for both views: a revision by itself is rows that are all unchanged. */
const rows = computed<{ kind: 'same' | 'removed' | 'added'; item: Cue }[]>(() => {
  if (!comparison.value) return shownCues.value.map((item) => ({ kind: 'same', item }))
  return changesOnly.value ? comparison.value.rows.filter((r) => r.kind !== 'same') : comparison.value.rows
})

function revisionLabel(r: Revision): string {
  return `Revision ${r.revision} · ${r.origin} · ${r.createdAt.slice(0, 10)}`
}

const input =
  'h-9 rounded-control border border-border bg-background px-3 text-foreground placeholder:text-muted-foreground/60 focus-visible:focus-ring'
const card = 'rounded-card border border-border bg-card'
const th = 'px-4 py-2.5 text-left text-xs font-medium uppercase tracking-wider text-muted-foreground'
const td = 'border-t border-border px-4 py-2 align-top'
const mark = { same: '', removed: '−', added: '+' }
const tint = { same: '', removed: 'bg-destructive/10', added: 'bg-success/10' }
</script>

<template>
  <main class="mx-auto flex max-w-[1200px] flex-col gap-6 p-8">
    <header class="flex flex-col gap-1.5">
      <h1 class="text-3xl font-semibold leading-tight tracking-tight">Subtitles</h1>
      <p class="text-[15px] text-muted-foreground">The subtitle tracks of an asset, with every revision.</p>
    </header>

    <form class="flex flex-wrap items-end gap-3" novalidate @submit.prevent="open(wanted)">
      <Field.Root class="flex flex-col gap-1.5">
        <Field.Label class="font-medium">Asset id or legacy id</Field.Label>
        <Field.Input v-model="wanted" :class="[input, 'w-80 font-mono']" placeholder="AS-… or VX-123" autocomplete="off" spellcheck="false" />
      </Field.Root>
      <button
        type="submit"
        class="h-9 rounded-control bg-primary px-4 font-medium text-primary-foreground hover:bg-primary/90 focus-visible:focus-ring disabled:pointer-events-none disabled:opacity-50"
        :disabled="loading || !wanted.trim()"
      >
        {{ loading ? 'Opening…' : 'Open' }}
      </button>
    </form>

    <p v-if="error" role="alert" class="rounded-control border border-destructive/40 bg-destructive/10 px-4 py-3">
      {{ error.message }}
    </p>
    <p v-if="notFound" data-test="not-found" class="text-muted-foreground">
      No asset has this id, or you may not see it.
    </p>

    <section v-if="candidates.length" :class="[card, 'flex flex-col gap-3 p-6']" aria-label="Matching assets">
      <h2 class="font-medium">Several assets have this legacy id</h2>
      <ul class="flex flex-col gap-1">
        <li v-for="c in candidates" :key="c.id">
          <button
            type="button"
            data-test="candidate"
            class="flex w-full flex-col rounded-control px-3 py-2 text-left hover:bg-accent focus-visible:focus-ring"
            @click="choose(c)"
          >
            <span class="font-medium">{{ c.title }}</span>
            <span class="font-mono text-xs text-muted-foreground">{{ c.id }}</span>
          </button>
        </li>
      </ul>
    </section>

    <template v-if="asset">
      <section :class="[card, 'flex flex-col gap-1 p-6']" aria-label="Asset" data-test="asset">
        <h2 class="text-lg font-medium leading-snug">{{ asset.title }}</h2>
        <p class="font-mono text-xs text-muted-foreground">{{ [asset.id, ...asset.legacyIds].join(' · ') }}</p>
      </section>

      <p v-if="tracks.length === 0" data-test="no-tracks" class="text-muted-foreground">This asset has no subtitles.</p>

      <div v-else class="flex flex-wrap items-start gap-6">
        <nav class="flex min-w-0 flex-[1_1_220px] flex-col gap-1" aria-label="Subtitle tracks">
          <button
            v-for="t in tracks"
            :key="t.renditionId"
            type="button"
            data-test="track"
            class="flex flex-col rounded-control px-3 py-2 text-left hover:bg-accent focus-visible:focus-ring"
            :class="{ 'bg-accent': t.renditionId === track?.renditionId }"
            :aria-current="t.renditionId === track?.renditionId ? 'true' : undefined"
            @click="selectTrack(t.renditionId)"
          >
            <span class="font-medium">{{ trackLabel(t) }}</span>
            <span class="text-xs text-muted-foreground">
              {{ t.revisions.length === 1 ? '1 revision' : `${t.revisions.length} revisions` }}
            </span>
          </button>
        </nav>

        <section v-if="track && shown" class="flex min-w-0 flex-[999_1_600px] flex-col gap-4" aria-label="Revisions">
          <div class="flex flex-wrap items-end gap-4">
            <Field.Root class="flex flex-col gap-1.5">
              <Field.Label class="font-medium">Revision</Field.Label>
              <Field.Select data-test="shown" :class="input" :model-value="shown.id" @update:model-value="show">
                <option v-for="r in track.revisions" :key="r.id" :value="r.id" :selected="r.id === shown.id">
                  {{ revisionLabel(r) }}
                </option>
              </Field.Select>
            </Field.Root>
            <Field.Root class="flex flex-col gap-1.5">
              <Field.Label class="font-medium">Compare with</Field.Label>
              <Field.Select
                data-test="base"
                :class="input"
                :model-value="base?.id ?? ''"
                :disabled="track.revisions.length < 2"
                @update:model-value="compare"
              >
                <option value="" :selected="!base">Nothing</option>
                <template v-for="r in track.revisions" :key="r.id">
                  <option v-if="r.id !== shown.id" :value="r.id" :selected="r.id === base?.id">
                    {{ revisionLabel(r) }}
                  </option>
                </template>
              </Field.Select>
            </Field.Root>
            <template v-if="comparison">
              <div class="flex h-9 items-center gap-4">
                <CheckOption v-model="ignoreTiming" data-test="ignore-timing">Ignore timing</CheckOption>
                <CheckOption v-model="changesOnly" data-test="changes-only">Changes only</CheckOption>
              </div>
            </template>
          </div>

          <p class="font-mono text-xs text-muted-foreground" data-test="details">
            <template v-if="comparison">
              Revision {{ comparison.from.revision }} → {{ comparison.to.revision }}:
              <span class="text-success">+{{ comparison.added }}</span>
              <span class="text-destructive"> −{{ comparison.removed }}</span> cues
            </template>
            <template v-else>
              {{ [shown.id, shown.model, shown.createdBy && `by ${shown.createdBy}`, shown.createdAt, `${shownCues.length} cues`].filter(Boolean).join(' · ') }}
            </template>
          </p>

          <div :class="[card, 'overflow-hidden', { 'opacity-60': cuesLoading }]">
            <div class="overflow-x-auto">
              <table class="w-full min-w-[560px] border-collapse">
                <thead>
                  <tr>
                    <th :class="[th, 'w-8 !px-2']"><span class="sr-only">Change</span></th>
                    <th :class="[th, 'w-64']">Time</th>
                    <th :class="th">Text</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="(r, i) in rows" :key="i" data-test="cue" :data-kind="r.kind" :class="tint[r.kind]">
                    <td :class="[td, '!px-2 text-center font-mono', r.kind === 'added' ? 'text-success' : 'text-destructive']">
                      {{ mark[r.kind] }}
                    </td>
                    <td :class="[td, 'whitespace-nowrap font-mono text-[13px] text-muted-foreground']">
                      {{ timecode(r.item.startMs) }} → {{ timecode(r.item.endMs) }}
                    </td>
                    <td :class="[td, 'whitespace-pre-wrap']">
                      <span v-if="r.item.speaker" class="text-muted-foreground">{{ r.item.speaker }}: </span>{{ r.item.text }}
                    </td>
                  </tr>
                  <tr v-if="rows.length === 0">
                    <td :class="[td, 'text-muted-foreground']" colspan="3">
                      {{ cuesLoading ? 'Loading…' : comparison ? 'No differences.' : 'No cues.' }}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </section>
      </div>
    </template>
  </main>
</template>
