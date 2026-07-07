<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{ percent?: number }>(), { percent: 0 })

const width = computed(() => `${Math.min(100, Math.max(0, props.percent)).toFixed(1)}%`)
</script>

<template>
  <div class="h-2 w-full overflow-hidden rounded-full bg-zinc-800">
    <div
      class="h-full rounded-full bg-indigo-500 transition-[width] duration-500 ease-out"
      :style="{ width }"
    />
  </div>
</template>
