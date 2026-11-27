<script setup lang="ts">
import { onMounted } from 'vue'
import { useAssets } from '../composables/useAssets'

const { assets, loading, loaded, error, hasMore, reload, more } = useAssets()

onMounted(reload)

const th = 'px-4 py-2.5 text-left text-xs font-medium uppercase tracking-wider text-muted-foreground'
const td = 'border-t border-border px-4 py-3 align-middle'
</script>

<template>
  <main class="mx-auto flex max-w-[1200px] flex-col gap-6 p-8">
    <header class="flex flex-col gap-1.5">
      <h1 class="text-3xl font-semibold leading-tight tracking-tight">Assets</h1>
      <p class="text-[15px] text-muted-foreground">The assets you may see, newest first.</p>
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
        @click="loaded ? more() : reload()"
      >
        Try again
      </button>
    </div>

    <section class="flex flex-col gap-3" aria-label="Assets">
      <div class="overflow-hidden rounded-card border border-border bg-card">
        <div class="overflow-x-auto">
          <table class="w-full min-w-[640px] border-collapse">
            <thead>
              <tr>
                <th :class="th">Title</th>
                <th :class="th">Type</th>
                <th :class="th">Legacy ids</th>
                <th :class="th">Created</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="a in assets" :key="a.id" data-test="asset">
                <td :class="td">
                  <div class="flex flex-col">
                    <span class="font-medium">{{ a.title }}</span>
                    <span class="font-mono text-xs text-muted-foreground">{{ a.id }}</span>
                  </div>
                </td>
                <td :class="td">{{ a.type.name }}</td>
                <td :class="[td, 'font-mono text-[13px]']">{{ a.legacyIds.join(', ') || '—' }}</td>
                <td :class="[td, 'font-mono text-[13px]']">{{ a.createdAt.slice(0, 10) }}</td>
              </tr>
              <tr v-if="loaded && assets.length === 0">
                <td :class="[td, 'text-muted-foreground']" colspan="4">No assets you can see.</td>
              </tr>
              <tr v-if="loading && !loaded">
                <td :class="[td, 'text-muted-foreground']" colspan="4">Loading…</td>
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
    </section>
  </main>
</template>
