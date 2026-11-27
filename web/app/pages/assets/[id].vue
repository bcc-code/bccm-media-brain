<script setup lang="ts">
import AssetView from '../../components/AssetView.vue'

const route = useRoute()
</script>

<template>
  <AssetView :id="String(route.params.id)" />
</template>
