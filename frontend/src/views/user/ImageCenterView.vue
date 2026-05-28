<template>
  <AppLayout>
    <div class="image-center-page">
      <div class="image-center-shell">
        <form class="card image-form" @submit.prevent="submitTask">
          <div class="mb-5 border-b border-gray-100 pb-4 dark:border-dark-700">
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('imageCenter.title') }}</h3>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('imageCenter.description') }}</p>
          </div>

          <div class="mb-5 flex rounded-lg bg-gray-100 p-1 dark:bg-dark-700">
            <button
              type="button"
              class="flex-1 rounded-md px-3 py-2 text-sm font-medium transition"
              :class="mode === 'generation' ? activeTabClass : inactiveTabClass"
              @click="mode = 'generation'"
            >
              {{ t('imageCenter.generation') }}
            </button>
            <button
              type="button"
              class="flex-1 rounded-md px-3 py-2 text-sm font-medium transition"
              :class="mode === 'edit' ? activeTabClass : inactiveTabClass"
              @click="mode = 'edit'"
            >
              {{ t('imageCenter.edit') }}
            </button>
          </div>

          <div class="space-y-4">
            <label class="block">
              <span class="input-label">{{ t('imageCenter.apiKey') }}</span>
              <select v-model.number="form.apiKeyId" class="input mt-1.5">
                <option :value="0">-</option>
                <option v-for="key in apiKeys" :key="key.id" :value="key.id">
                  {{ apiKeyOptionLabel(key) }}
                </option>
              </select>
            </label>

            <div class="grid gap-4 sm:grid-cols-[1fr_132px] xl:grid-cols-1">
              <label class="block">
                <span class="input-label">{{ t('imageCenter.model') }}</span>
                <input v-model.trim="form.model" class="input mt-1.5" autocomplete="off" />
              </label>
              <label class="block">
                <span class="input-label">{{ t('imageCenter.size') }}</span>
                <select v-model="form.size" class="input mt-1.5">
                  <option v-for="option in sizeOptions" :key="option.value" :value="option.value">
                    {{ option.label }}
                  </option>
                </select>
              </label>
            </div>

            <label class="block">
              <span class="input-label">{{ t('imageCenter.prompt') }}</span>
              <textarea v-model.trim="form.prompt" rows="7" class="input mt-1.5 min-h-[160px] resize-y" />
            </label>

            <div class="grid gap-3 sm:grid-cols-2">
              <label class="block">
                <span class="input-label">{{ t('imageCenter.count') }}</span>
                <input v-model.number="form.n" type="number" min="1" max="10" class="input mt-1.5" />
              </label>
              <label class="block">
                <span class="input-label">{{ t('imageCenter.quality') }}</span>
                <select v-model="form.quality" class="input mt-1.5">
                  <option v-for="option in qualityOptions" :key="option.value" :value="option.value">
                    {{ option.label }}
                  </option>
                </select>
              </label>
            </div>

            <div v-if="mode === 'edit'" class="space-y-3">
              <div class="rounded-lg border border-dashed border-gray-300 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-900/60">
                <input ref="imageInput" type="file" accept="image/*" multiple class="sr-only" @change="onImagesChange" />
                <div class="flex flex-col gap-3">
                  <div class="min-w-0">
                    <p class="text-sm font-medium text-gray-800 dark:text-dark-100">{{ t('imageCenter.images') }}</p>
                    <p class="mt-1 truncate text-xs text-gray-500 dark:text-dark-400">
                      {{ imageFileNames.length ? imageFileNames.join(', ') : t('imageCenter.imageRequired') }}
                    </p>
                  </div>
                  <div class="flex flex-wrap gap-2">
                    <button type="button" class="btn btn-secondary" @click="imageInput?.click()">
                      <Icon name="upload" size="sm" />
                      {{ t('imageCenter.chooseImages') }}
                    </button>
                    <button v-if="imageFiles.length" type="button" class="btn btn-ghost" @click="clearImageFiles">
                      {{ t('imageCenter.clear') }}
                    </button>
                  </div>
                </div>
              </div>

              <div class="rounded-lg border border-dashed border-gray-300 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-900/60">
                <input ref="maskInput" type="file" accept="image/*" class="sr-only" @change="onMaskChange" />
                <div class="flex flex-col gap-3">
                  <div class="min-w-0">
                    <p class="text-sm font-medium text-gray-800 dark:text-dark-100">{{ t('imageCenter.mask') }}</p>
                    <p class="mt-1 truncate text-xs text-gray-500 dark:text-dark-400">{{ maskFile?.name || '-' }}</p>
                  </div>
                  <div class="flex flex-wrap gap-2">
                    <button type="button" class="btn btn-secondary" @click="maskInput?.click()">
                      <Icon name="upload" size="sm" />
                      {{ t('imageCenter.chooseMask') }}
                    </button>
                    <button v-if="maskFile" type="button" class="btn btn-ghost" @click="clearMaskFile">
                      {{ t('imageCenter.clear') }}
                    </button>
                  </div>
                </div>
              </div>
            </div>

            <button type="submit" class="btn btn-primary w-full justify-center" :disabled="submitting">
              <Icon name="sparkles" size="sm" />
              {{ submitting ? t('imageCenter.submitting') : t('imageCenter.submit') }}
            </button>
          </div>
        </form>

        <section class="card image-task-list overflow-hidden">
          <div class="flex flex-col gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('imageCenter.tasks') }}</h3>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
                {{ statusCounts.pending + statusCounts.running + statusCounts.succeeded + statusCounts.failed }}
              </p>
            </div>
            <button type="button" class="btn btn-secondary" :disabled="loadingTasks" @click="loadTasks()">
              <Icon name="refresh" size="sm" :class="loadingTasks ? 'animate-spin' : ''" />
            </button>
          </div>

          <div class="flex flex-wrap gap-2 border-b border-gray-100 px-5 py-3 dark:border-dark-700">
            <span class="badge badge-warning">{{ t('imageCenter.status.pending') }} {{ statusCounts.pending }}</span>
            <span class="badge badge-primary">{{ t('imageCenter.status.running') }} {{ statusCounts.running }}</span>
            <span class="badge badge-success">{{ t('imageCenter.status.succeeded') }} {{ statusCounts.succeeded }}</span>
            <span class="badge badge-danger">{{ t('imageCenter.status.failed') }} {{ statusCounts.failed }}</span>
          </div>

          <div v-if="loadingTasks && !tasks.length" class="flex min-h-56 flex-1 items-center justify-center">
            <div class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"></div>
          </div>

          <div v-else-if="!tasks.length" class="flex min-h-56 flex-1 items-center justify-center px-5 py-12 text-sm text-gray-500 dark:text-gray-400">
            {{ t('imageCenter.empty') }}
          </div>

          <div v-else class="task-scroll divide-y divide-gray-100 dark:divide-dark-700">
            <button
              v-for="task in tasks"
              :key="task.id"
              type="button"
              class="flex w-full items-start gap-4 px-5 py-4 text-left transition hover:bg-gray-50 dark:hover:bg-dark-700/60"
              :class="selectedTask?.id === task.id ? 'bg-gray-50 dark:bg-dark-700/60' : ''"
              @click="selectTask(task)"
            >
              <span class="mt-1 h-2.5 w-2.5 flex-shrink-0 rounded-full" :class="statusDotClass(task.status)" />
              <span class="min-w-0 flex-1">
                <span class="block truncate text-sm font-medium text-gray-900 dark:text-white">{{ task.prompt }}</span>
                <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">
                  #{{ task.id }} · {{ task.model }} · {{ t(`imageCenter.status.${task.status}`) }} · {{ formatTime(task.created_at) }}
                </span>
                <span v-if="task.error_message" class="mt-1 block truncate text-xs text-red-600 dark:text-red-400">{{ task.error_message }}</span>
              </span>
            </button>
          </div>
        </section>
      </div>

      <section class="card result-panel">
        <div class="mb-4 flex items-center justify-between gap-4">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('imageCenter.result') }}</h2>
          <span v-if="selectedTask" class="text-sm text-gray-500 dark:text-gray-400">#{{ selectedTask.id }}</span>
        </div>

        <div v-if="!selectedTask" class="py-10 text-center text-sm text-gray-500 dark:text-gray-400">
          {{ t('imageCenter.noResult') }}
        </div>

        <div v-else class="space-y-6">
          <div v-if="resultImages.length" class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <figure v-for="(image, index) in resultImages" :key="image.src + index" class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-700">
              <img :src="image.src" class="aspect-square w-full bg-gray-100 object-contain dark:bg-dark-900" />
              <figcaption class="flex items-center justify-between gap-3 px-3 py-2">
                <span class="truncate text-xs text-gray-500 dark:text-gray-400">{{ image.label }}</span>
                <button type="button" class="rounded p-1 text-gray-500 hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-dark-700 dark:hover:text-white" @click="downloadImage(image.src, image.label)">
                  <Icon name="download" size="sm" />
                </button>
              </figcaption>
            </figure>
          </div>

          <div v-else class="py-8 text-center text-sm text-gray-500 dark:text-gray-400">
            {{ selectedTask.error_message || t('imageCenter.noResult') }}
          </div>

          <div v-if="inputImages.length || inputMask" class="space-y-3">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('imageCenter.inputs') }}</h3>
            <div class="grid gap-3 sm:grid-cols-3 lg:grid-cols-6">
              <img v-for="image in inputImages" :key="image.src" :src="image.src" class="aspect-square rounded border border-gray-200 object-cover dark:border-dark-700" />
              <img v-if="inputMask" :src="inputMask.src" class="aspect-square rounded border border-dashed border-gray-300 object-cover dark:border-dark-600" />
            </div>
          </div>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import imageTasksAPI from '@/api/imageTasks'
import keysAPI from '@/api/keys'
import type { ApiKey, ImageTask, ImageTaskStatus, ImageTaskUpload } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()

const activeTabClass = 'bg-white text-gray-900 shadow-sm dark:bg-dark-900 dark:text-white'
const inactiveTabClass = 'text-gray-600 hover:text-gray-900 dark:text-gray-300 dark:hover:text-white'

const sizeOptions = [
  { value: 'auto', label: 'auto' },
  { value: '1024x1024', label: '1024x1024' },
  { value: '1024x1536', label: '1024x1536' },
  { value: '1536x1024', label: '1536x1024' },
  { value: '2048x2048', label: '2048x2048' },
  { value: '2048x1152', label: '2048x1152' },
  { value: '1152x2048', label: '1152x2048' },
  { value: '3840x2160', label: '3840x2160' },
  { value: '2160x3840', label: '2160x3840' },
]
const qualityOptions = [
  { value: 'auto', label: 'auto' },
  { value: 'low', label: 'low' },
  { value: 'medium', label: 'medium' },
  { value: 'high', label: 'high' },
]

const mode = ref<'generation' | 'edit'>('generation')
const form = reactive({
  apiKeyId: 0,
  model: 'gpt-image-2',
  prompt: '',
  size: 'auto',
  n: 1,
  quality: 'auto',
})
const imageInput = ref<HTMLInputElement | null>(null)
const maskInput = ref<HTMLInputElement | null>(null)
const imageFiles = ref<File[]>([])
const maskFile = ref<File | null>(null)
const apiKeys = ref<ApiKey[]>([])
const tasks = ref<ImageTask[]>([])
const selectedTask = ref<ImageTask | null>(null)
const submitting = ref(false)
const loadingTasks = ref(false)
let pollTimer: number | undefined

const imageFileNames = computed(() => imageFiles.value.map(file => file.name))
const hasActiveTasks = computed(() => tasks.value.some(task => task.status === 'pending' || task.status === 'running'))
const statusCounts = computed(() => {
  return tasks.value.reduce(
    (counts, task) => {
      counts[task.status] += 1
      return counts
    },
    { pending: 0, running: 0, succeeded: 0, failed: 0 } as Record<ImageTaskStatus, number>,
  )
})
const resultImages = computed(() => extractResultImages(selectedTask.value))
const selectedApiKey = computed(() => apiKeys.value.find(key => key.id === form.apiKeyId)?.key || '')
const inputImages = computed(() => extractUploads(selectedTask.value?.input_images_json))
const inputMask = computed(() => {
  const raw = selectedTask.value?.input_mask_json
  if (!raw) return null
  try {
    return uploadToImage(JSON.parse(raw) as ImageTaskUpload)
  } catch {
    return null
  }
})

onMounted(async () => {
  await Promise.all([loadKeys(), loadTasks()])
  pollTimer = window.setInterval(() => {
    if (hasActiveTasks.value) loadTasks({ silent: true })
  }, 3000)
})

onBeforeUnmount(() => {
  if (pollTimer) window.clearInterval(pollTimer)
})

watch(tasks, (items) => {
  if (!selectedTask.value && items.length) {
    selectTask(items[0])
    return
  }

  const current = selectedTask.value
  const next = current && items.find(task => task.id === current.id)
  if (!current || !next) return
  if (current.status !== next.status && next.status !== 'pending' && next.status !== 'running') {
    selectTask(next)
    return
  }
  selectedTask.value = { ...current, ...next }
})

async function loadKeys() {
  try {
    const res = await keysAPI.list(1, 100, { status: 'active' })
    apiKeys.value = res.items
    if (!form.apiKeyId && res.items.length) form.apiKeyId = res.items[0].id
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  }
}

async function loadTasks(options: { silent?: boolean } = {}) {
  if (!options.silent) loadingTasks.value = true
  try {
    const res = await imageTasksAPI.list(1, 50)
    tasks.value = res.items
  } catch (err: unknown) {
    if (!options.silent) appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    if (!options.silent) loadingTasks.value = false
  }
}

async function submitTask() {
  if (!selectedApiKey.value || !form.model || !form.prompt) {
    appStore.showError(t('imageCenter.required'))
    return
  }
  if (mode.value === 'edit' && imageFiles.value.length === 0) {
    appStore.showError(t('imageCenter.imageRequired'))
    return
  }

  submitting.value = true
  try {
    const payload = {
      api_key: selectedApiKey.value,
      model: form.model,
      prompt: form.prompt,
      size: form.size,
      n: form.n,
      quality: form.quality,
    }
    const task = mode.value === 'generation'
      ? await imageTasksAPI.createGeneration(payload)
      : await imageTasksAPI.createEdit({ ...payload, images: imageFiles.value, mask: maskFile.value })
    selectedTask.value = await imageTasksAPI.get(task.id)
    await loadTasks({ silent: true })
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    submitting.value = false
  }
}

async function selectTask(task: ImageTask) {
  selectedTask.value = task
  try {
    selectedTask.value = await imageTasksAPI.get(task.id)
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  }
}

function onImagesChange(event: Event) {
  const input = event.target as HTMLInputElement
  imageFiles.value = Array.from(input.files || []).slice(0, 10)
}

function onMaskChange(event: Event) {
  const input = event.target as HTMLInputElement
  maskFile.value = input.files?.[0] || null
}

function clearImageFiles() {
  imageFiles.value = []
  if (imageInput.value) imageInput.value.value = ''
}

function clearMaskFile() {
  maskFile.value = null
  if (maskInput.value) maskInput.value.value = ''
}

function apiKeyOptionLabel(key: ApiKey) {
  return `${key.name}（${key.group?.name || t('keys.noGroup')}）`
}

function statusDotClass(status: ImageTaskStatus) {
  return {
    pending: 'bg-amber-400',
    running: 'bg-blue-500',
    succeeded: 'bg-green-500',
    failed: 'bg-red-500',
  }[status]
}

function formatTime(raw: string) {
  return new Date(raw).toLocaleString()
}

function extractResultImages(task: ImageTask | null): Array<{ src: string; label: string }> {
  if (!task?.response_json) return []
  try {
    const parsed = JSON.parse(task.response_json) as { data?: Array<{ b64_json?: string; url?: string }> }
    return (parsed.data || []).flatMap((item, index) => {
      if (item.b64_json) return [{ src: `data:image/png;base64,${item.b64_json}`, label: `image-${index + 1}.png` }]
      if (item.url) return [{ src: item.url, label: `image-${index + 1}` }]
      return []
    })
  } catch {
    return []
  }
}

function extractUploads(raw?: string): Array<{ src: string; label: string }> {
  if (!raw) return []
  try {
    return (JSON.parse(raw) as ImageTaskUpload[]).map(uploadToImage)
  } catch {
    return []
  }
}

function uploadToImage(upload: ImageTaskUpload) {
  return {
    src: `data:${upload.content_type || 'image/png'};base64,${upload.data_base64}`,
    label: upload.file_name || 'image.png',
  }
}

function downloadImage(src: string, label: string) {
  const link = document.createElement('a')
  link.href = src
  link.download = label.endsWith('.png') ? label : `${label}.png`
  link.click()
}
</script>

<style scoped>
.image-center-page {
  @apply mx-auto flex w-full max-w-7xl flex-col gap-6 px-4 py-6 sm:px-6 lg:px-8;
}

.image-center-shell {
  @apply grid gap-6 xl:grid-cols-[minmax(320px,420px)_minmax(0,1fr)] xl:items-start;
}

.image-form {
  @apply p-5 xl:sticky xl:top-24;
}

.image-task-list {
  @apply flex min-h-[420px] flex-col xl:h-[calc(100vh-64px-4rem)];
}

.task-scroll {
  @apply flex-1 overflow-y-auto;
}

.result-panel {
  @apply p-5;
}
</style>
