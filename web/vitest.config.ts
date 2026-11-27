import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

// Plain Vitest, without Nuxt: everything under test imports what it uses, so no auto-imports
// are needed. app.vue and pages/index.vue hold only Nuxt wiring and are left out of the gate.
export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'happy-dom',
    include: ['test/**/*.test.ts'],
    coverage: {
      provider: 'v8',
      include: ['app/utils/**', 'app/composables/**', 'app/components/**', 'app/pages/service-accounts.vue', 'app/pages/subtitles.vue', 'app/pages/users.vue', 'app/pages/assets.vue'],
      thresholds: { lines: 90, functions: 90, branches: 90, statements: 90 },
    },
  },
})
