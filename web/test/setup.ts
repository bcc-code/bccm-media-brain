import { RouterLinkStub, config } from '@vue/test-utils'

// Nuxt registers NuxtLink; without Nuxt a stub stands in, an <a> that records where it leads.
config.global.stubs = { NuxtLink: RouterLinkStub }
