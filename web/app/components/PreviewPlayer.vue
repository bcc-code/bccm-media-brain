<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { LAYER_COLORS, layerTag, type Layer } from '../composables/useAsset'
import { timecode } from '../composables/useSubtitles'

const props = defineProps<{
  /** The preview's address, or null when the asset has none. */
  url: string | null
  layers: Layer[]
  /** How far the timeline goes when there is no video to say so, in milliseconds. */
  duration: number
  showTags: boolean
}>()
/** Where the preview is, in milliseconds. */
const time = defineModel<number>('time', { required: true })

const video = ref<HTMLVideoElement | null>(null)
const failed = ref(false)
watch(
  () => props.url,
  () => (failed.value = false),
)

// timeupdate comes about four times a second, too seldom for a subtitle to change on time, so
// while the video plays the time is read every frame.
let frame = 0
function follow() {
  if (video.value) time.value = Math.round(video.value.currentTime * 1000)
  frame = requestAnimationFrame(follow)
}
function playing() {
  cancelAnimationFrame(frame)
  frame = requestAnimationFrame(follow)
}
function stopped() {
  cancelAnimationFrame(frame)
  if (video.value) time.value = Math.round(video.value.currentTime * 1000)
}
onBeforeUnmount(() => cancelAnimationFrame(frame))

function scrub(e: Event) {
  time.value = Number((e.target as HTMLInputElement).value)
}
</script>

<template>
  <div class="overflow-hidden rounded-card border border-border bg-card">
    <div class="relative aspect-video bg-black">
      <video
        v-if="url && !failed"
        ref="video"
        data-test="video"
        :src="url"
        controls
        preload="metadata"
        class="absolute inset-0 size-full"
        @play="playing"
        @pause="stopped"
        @seeked="stopped"
        @timeupdate="stopped"
        @ended="stopped"
        @error="failed = true"
      />
      <div v-else data-test="no-preview" class="absolute inset-0 flex flex-col items-center justify-center gap-2 p-6 text-center">
        <svg aria-hidden="true" class="size-10 text-[#737373]" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M10.66 6H14a2 2 0 0 1 2 2v2.5l5.248-3.062A.5.5 0 0 1 22 7.87v8.196" />
          <path d="M16 16a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h2" />
          <path d="m2 2 20 20" />
        </svg>
        <p class="font-medium">{{ failed ? 'The preview could not be loaded' : 'No preview' }}</p>
        <p class="max-w-md text-[13px] text-muted-foreground">
          {{
            failed
              ? 'Vidispine may be unavailable, or no longer have the file.'
              : 'A Vidispine import adds one from the lowres_watermarked shape.'
          }}
        </p>
      </div>

      <!--
        Above the video's own controls; the first layer is the lowest. Every layer keeps its box,
        empty or not, of the full width and at least two lines high, so a line changing never
        moves the others.
      -->
      <div
        class="pointer-events-none absolute inset-x-[5%] bottom-[14%] flex flex-col-reverse gap-1.5 text-center"
        aria-live="off"
        data-test="overlay"
      >
        <div
          v-for="l in layers"
          :key="l.revision.id"
          data-test="line"
          class="flex min-h-[calc(2lh+0.5rem)] w-full items-center gap-3 rounded bg-black/65 px-2.5 py-1 text-[clamp(13px,2.2vw,20px)] font-medium leading-snug"
          :style="{ color: LAYER_COLORS[l.slot] }"
        >
          <span
            v-if="showTags"
            data-test="tag"
            :title="layerTag(l.revision)"
            class="w-[clamp(7rem,22%,13rem)] shrink-0 truncate rounded-[3px] border border-current px-1 text-left font-mono text-[11px] font-normal"
            >{{ layerTag(l.revision) }}</span
          >
          <span class="min-w-0 flex-1 whitespace-pre-line"
            ><template v-for="(s, i) in l.text" :key="i"
              ><span v-if="s.changed" data-test="changed" class="rounded-[3px] bg-current/25">{{
                s.text
              }}</span
              ><template v-else>{{ s.text }}</template></template
            ></span
          >
          <!-- Balances the tag, so the text is centred on the picture. -->
          <span v-if="showTags" aria-hidden="true" class="w-[clamp(7rem,22%,13rem)] shrink-0" />
        </div>
      </div>
    </div>

    <div v-if="!url || failed" class="flex items-center gap-3 border-t border-border px-4 py-3">
      <span class="shrink-0 font-mono text-xs">{{ timecode(time) }}</span>
      <input
        type="range"
        data-test="timeline"
        aria-label="Time"
        class="w-full accent-foreground"
        min="0"
        :max="duration"
        step="100"
        :value="time"
        :disabled="duration === 0"
        @input="scrub"
      />
      <span class="shrink-0 font-mono text-xs text-muted-foreground">{{ timecode(duration) }}</span>
    </div>
  </div>
</template>
