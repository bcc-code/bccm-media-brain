import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

// Plain Vitest, without Nuxt: everything under test imports what it uses, so no auto-imports
// are needed (NuxtLink is stubbed in test/setup.ts). app.vue, pages/index.vue and
// pages/assets/[id].vue hold only Nuxt wiring and are left out of the gate.
export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'happy-dom',
    include: ['test/**/*.test.ts'],
    setupFiles: ['test/setup.ts'],
    coverage: {
      provider: 'v8',
      include: ['app/utils/**', 'app/composables/**', 'app/components/**', 'app/pages/service-accounts.vue', 'app/pages/subtitles.vue', 'app/pages/users.vue', 'app/pages/assets/index.vue'],
      thresholds: { lines: 90, functions: 90, branches: 90, statements: 90 },
    },
  },
})
