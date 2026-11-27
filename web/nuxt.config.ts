import tailwindcss from '@tailwindcss/vite'

// Where `make dev` serves the API. The dev server forwards /query and /media there, so the page and the
// API share an origin, as they do behind the proxy in a deployment.
const api = process.env.MEDIABRAIN_API ?? 'http://127.0.0.1:8080'

export default defineNuxtConfig({
  compatibilityDate: '2026-11-01',
  // A static app: sign-in is the proxy's, and every request for data goes to /query.
  ssr: false,
  css: ['~/assets/css/main.css'],
  vite: { plugins: [tailwindcss()] },
  nitro: { devProxy: { '/query': { target: `${api}/query` }, '/media': { target: `${api}/media` } } },
  app: {
    // The Go server serves the build from here (internal/webui); /query stays at the root.
    baseURL: '/admin/',
    head: {
      title: 'MediaBrain',
      htmlAttrs: { lang: 'en' },
      meta: [{ name: 'color-scheme', content: 'dark' }],
    },
  },
})
