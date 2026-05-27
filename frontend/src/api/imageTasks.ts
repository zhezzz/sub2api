import { apiClient } from './client'
import type { ImageTask, PaginatedResponse } from '@/types'

export interface CreateImageGenerationTaskRequest {
  api_key: string
  model: string
  prompt: string
  size?: string
  n?: number
  quality?: string
}

export interface CreateImageEditTaskRequest extends CreateImageGenerationTaskRequest {
  images: File[]
  mask?: File | null
}

export async function createGeneration(payload: CreateImageGenerationTaskRequest): Promise<ImageTask> {
  const { data } = await apiClient.post<ImageTask>('/image-tasks/generations', payload)
  return data
}

export async function createEdit(payload: CreateImageEditTaskRequest): Promise<ImageTask> {
  const form = new FormData()
  form.append('api_key', payload.api_key)
  form.append('model', payload.model)
  form.append('prompt', payload.prompt)
  if (payload.size) form.append('size', payload.size)
  if (payload.n) form.append('n', String(payload.n))
  if (payload.quality) form.append('quality', payload.quality)
  for (const image of payload.images) {
    form.append('image', image)
  }
  if (payload.mask) {
    form.append('mask', payload.mask)
  }
  const { data } = await apiClient.post<ImageTask>('/image-tasks/edits', form, {
    timeout: 120000,
  })
  return data
}

export async function list(page = 1, pageSize = 20): Promise<PaginatedResponse<ImageTask>> {
  const { data } = await apiClient.get<PaginatedResponse<ImageTask>>('/image-tasks', {
    params: { page, page_size: pageSize },
  })
  return data
}

export async function get(id: number): Promise<ImageTask> {
  const { data } = await apiClient.get<ImageTask>(`/image-tasks/${id}`)
  return data
}

export const imageTasksAPI = {
  createGeneration,
  createEdit,
  list,
  get,
}

export default imageTasksAPI
