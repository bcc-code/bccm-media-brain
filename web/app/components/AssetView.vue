<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import LayerPicker from './LayerPicker.vue'
import PreviewPlayer from './PreviewPlayer.vue'
import { useAsset } from '../composables/useAsset'

const props = defineProps<{ id: string }>()

const { asset, notFound, tracks, loading, error, selected, time, highlight, layers, preview, duration, load, toggle } = useAsset()
const showTags = ref(true)

watch(() => props.id, load, { immediate: true })

const level: Record<string, string> = {
  VIEW: 'View access',
  DOWNLOAD_ORIGINAL: 'Download access',
  EDIT: 'Edit access',
  DELETE: 'Delete access',
  ADMIN: 'Admin access',
}

const details = computed(() => {
  const a = asset.value
  if (!a) return []
  return [
    { label: 'Type', value: a.type.name },
    { label: 'Original language', value: a.originalLanguage, mono: true },
    { label: 'Captured', value: a.capturedAt && when(a.capturedAt), mono: true },
    { label: 'Camera', value: a.camera },
    { label: 'Reel', value: a.reel, mono: true },
    { label: 'Legacy ids', value: a.legacyIds.join(', '), mono: true },
    { label: 'Created', value: when(a.createdAt), mono: true },
    { label: 'Updated', value: when(a.updatedAt), mono: true },
  ]
})

/** Top-level metadata keys; an object or a list is shown as JSON. */
const metadata = computed(() =>
  Object.entries(asset.value?.metadata ?? {}).map(([key, value]) =>
    value !== null && typeof value === 'object' ? { key, json: JSON.stringify(value, null, 2) } : { key, text: String(value) },
  ),
)

function when(iso: string): string {
  return iso.slice(0, 16).replace('T', ' ')
}

function sentence(s: string): string {
  return s.charAt(0) + s.slice(1).toLowerCase().replaceAll('_', ' ')
}

const card = 'flex flex-col gap-3 rounded-card border border-border bg-card p-4'
const badge = 'rounded-[6px] px-1.5 py-0.5 text-xs font-medium'
</script>

<template>
  <main class="mx-auto flex max-w-[1280px] flex-col gap-6 p-8">
    <nav aria-label="Breadcrumb" class="flex items-center gap-1.5 text-[13px] text-muted-foreground">
      <NuxtLink to="/assets" class="hover:text-foreground focus-visible:focus-ring">Assets</NuxtLink>
      <svg aria-hidden="true" class="size-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="m9 18 6-6-6-6" />
      </svg>
      <span class="font-mono text-foreground">{{ id }}</span>
    </nav>

    <p v-if="error" role="alert" class="rounded-control border border-destructive/40 bg-destructive/10 px-4 py-3">
      {{ error.message }}
    </p>
    <p v-if="notFound" data-test="not-found" class="text-muted-foreground">No asset has this id, or you may not see it.</p>
    <p v-if="loading && !asset" class="text-muted-foreground">Loading…</p>

    <template v-if="asset">
      <header class="flex flex-col gap-2.5">
        <h1 class="text-3xl font-semibold leading-tight tracking-tight" data-test="title">{{ asset.title }}</h1>
        <div class="flex flex-wrap items-center gap-2">
          <span :class="[badge, 'bg-accent']">{{ asset.type.name }}</span>
          <span :class="[badge, 'border border-border-strong']" data-test="level">{{ level[asset.myLevel] ?? asset.myLevel }}</span>
          <span class="text-[13px] text-muted-foreground">Updated {{ when(asset.updatedAt) }} · revision {{ asset.revision }}</span>
        </div>
      </header>

      <div class="flex flex-wrap items-start gap-6">
        <section aria-label="Preview and subtitles" class="flex min-w-0 flex-[999_1_560px] flex-col gap-6">
          <PreviewPlayer v-model:time="time" :url="preview?.url ?? null" :layers="layers" :duration="duration" :show-tags="showTags" />
          <LayerPicker v-model:highlight="highlight" v-model:show-tags="showTags" :tracks="tracks" :selected="selected" @toggle="toggle" />
        </section>

        <aside aria-label="Metadata" class="flex min-w-0 flex-[1_1_320px] flex-col gap-6">
          <section :class="card" aria-labelledby="details-title">
            <h2 id="details-title" class="text-lg font-medium">Details</h2>
            <p v-if="asset.description" class="text-[13px] text-muted-foreground">{{ asset.description }}</p>
            <dl class="grid grid-cols-[minmax(0,0.9fr)_minmax(0,1.4fr)] gap-x-3 gap-y-2.5 text-[13px]">
              <template v-for="d in details" :key="d.label">
                <dt class="text-muted-foreground">{{ d.label }}</dt>
                <dd :class="['break-words', { 'font-mono': d.mono && d.value, 'text-muted-foreground': !d.value }]">{{ d.value || '—' }}</dd>
              </template>
            </dl>
          </section>

          <section :class="card" aria-labelledby="projects-title">
            <h2 id="projects-title" class="text-lg font-medium">Projects</h2>
            <p v-if="asset.projects.length === 0" class="text-[13px] text-muted-foreground">In no project you can see.</p>
            <ul v-else class="flex flex-col gap-2.5 text-[13px]">
              <li v-for="p in asset.projects" :key="`${p.project?.id}-${p.role}`" data-test="project" class="flex items-center justify-between gap-2">
                <span class="min-w-0">
                  {{ p.project?.name ?? 'A project' }}
                  <span v-if="p.project" class="font-mono text-xs text-muted-foreground">{{ p.project.code }}</span>
                </span>
                <span class="flex shrink-0 gap-1.5">
                  <span :class="[badge, 'bg-accent']">{{ sentence(p.role) }}</span>
                  <span :class="[badge, 'border border-border-strong']">{{ sentence(p.linkOrigin) }}</span>
                </span>
              </li>
            </ul>
          </section>

          <section :class="card" aria-labelledby="versions-title">
            <h2 id="versions-title" class="text-lg font-medium">Versions</h2>
            <p v-if="asset.versions.nodes.length === 0" class="text-[13px] text-muted-foreground">No versions yet.</p>
            <ul v-else class="flex flex-col text-[13px]">
              <li
                v-for="v in [...asset.versions.nodes].reverse()"
                :key="v.id"
                data-test="version"
                class="flex flex-col gap-2 border-t border-border py-3 first:border-t-0 first:pt-0 last:pb-0"
              >
                <div class="flex items-center justify-between gap-2">
                  <span><span class="font-mono">v{{ v.number }}</span> {{ v.label }}</span>
                  <span :class="[badge, v.status === 'FINAL' || v.status === 'APPROVED' ? 'bg-success/15 text-success' : 'bg-accent']">{{
                    sentence(v.status)
                  }}</span>
                </div>
                <div v-if="v.renditions.length" class="flex flex-wrap gap-1.5">
                  <span v-for="r in v.renditions" :key="r.id" :class="[badge, 'border border-border font-normal']" data-test="rendition">
                    {{ [sentence(r.kind), r.purpose, r.language].filter(Boolean).join(' · ') }}
                  </span>
                </div>
              </li>
            </ul>
          </section>

          <section :class="card" aria-labelledby="metadata-title">
            <h2 id="metadata-title" class="text-lg font-medium">Metadata</h2>
            <p v-if="metadata.length === 0" class="text-[13px] text-muted-foreground">None.</p>
            <ul v-else class="flex flex-col gap-2 font-mono text-xs">
              <li v-for="m in metadata" :key="m.key" data-test="metadata">
                <details v-if="m.json">
                  <summary class="cursor-pointer text-muted-foreground hover:text-foreground">{{ m.key }}</summary>
                  <pre class="mt-1.5 max-h-80 overflow-auto rounded-control bg-background p-2.5">{{ m.json }}</pre>
                </details>
                <div v-else class="grid grid-cols-[minmax(0,0.9fr)_minmax(0,1.4fr)] gap-x-3">
                  <span class="text-muted-foreground">{{ m.key }}</span>
                  <span class="break-words">{{ m.text }}</span>
                </div>
              </li>
            </ul>
          </section>
        </aside>
      </div>
    </template>
  </main>
</template>
