<template>
  <div class="min-h-screen">
    <header class="flex h-14 items-center gap-6 border-b border-border bg-card px-6">
      <span class="font-semibold">MediaBrain</span>
      <nav class="flex items-center gap-1" aria-label="Main">
        <NuxtLink
          to="/service-accounts"
          class="rounded-control px-3 py-1.5 font-medium text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:focus-ring"
          active-class="bg-accent !text-foreground"
        >
          Service accounts
        </NuxtLink>
        <NuxtLink
          to="/subtitles"
          class="rounded-control px-3 py-1.5 font-medium text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:focus-ring"
          active-class="bg-accent !text-foreground"
        >
          Subtitles
        </NuxtLink>
      </nav>
    </header>
    <NuxtPage />
  </div>
</template>
