export type Tool = 'convert' | 'merge' | 'compress' | 'media'
export type ConvertTarget = 'pdf' | 'docx'
export type CompressQuality = 'fast' | 'low' | 'medium' | 'high'
export type ImageFormat = 'jpg' | 'png' | 'webp' | 'gif' | 'svg'

export class ApiError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'ApiError'
  }
}

async function postFile(path: string, form: FormData, signal?: AbortSignal, onJob?: (id: string) => void): Promise<BlobResult> {
  const res = await fetch(path, { method: 'POST', body: form, signal })
  if (!res.ok) {
    let msg = `Request failed (${res.status})`
    const ctype = res.headers.get('content-type') ?? ''
    if (ctype.includes('application/json')) {
      const data = (await res.json()) as { error?: string }
      if (data.error) msg = data.error
    } else {
      const text = await res.text()
      if (text) msg = text
    }
    throw new ApiError(msg)
  }
  const blob = await res.blob()
  const headerName = res.headers.get('X-Filename')
  const headerJobId = res.headers.get('X-Job-ID') ?? undefined
  if (headerJobId) onJob?.(headerJobId)
  const disp = res.headers.get('Content-Disposition')
  const fromDisp = disp?.match(/filename="([^"]+)"/)?.[1]
  return {
    blob,
    filename: headerName || fromDisp || 'download',
    jobId: headerJobId,
    jobStatus: res.headers.get('X-Job-Status') ?? undefined,
  }
}

export type BlobResult = { blob: Blob; filename: string; jobId?: string; jobStatus?: string }
export type QueueJob = { jobId: string; operation: string; processor: string; priority: number; status: string; progress: number; schedule?: string; worker?: string; resourceRequirement?: string }
export type WorkerStat = { category: 'PDF' | 'DOCUMENT' | 'IMAGE' | 'MEDIA' | 'OCR'; total: number; active: number; available: number }
export type ResourceStats = { maxCPU: number; maxRAM: number; maxDisk: number; cpu: number; ram: number; disk: number }
export type QueueSnapshot = { queues: Record<'HIGH' | 'MEDIUM' | 'LOW', QueueJob[]>; workers: { available: number; active: number; total: number }; workerManager: WorkerStat[]; resources: ResourceStats }
export type ProcessorInfo = { name: string; category: string }
export type AnalysisReport = { type: string; size: number; dimensions?: string; pages?: number; text?: string; images?: number; metadata: string; encrypted?: string; complexity: string }
export type JobDetail = { jobId: string; status: string; progress: number; analysis: AnalysisReport; frame?: number; totalFrames?: number; fps?: number; speed?: number; elapsed?: string; remaining?: string; output?: string; outputBytes?: number; encoder?: string; error?: string }
export type MediaCapabilities = { encoder: string; hardware: boolean }

export async function getQueue(): Promise<QueueSnapshot> {
  const res = await fetch('/api/jobs')
  if (!res.ok) throw new ApiError(`Queue request failed (${res.status})`)
  const snapshot = (await res.json()) as Partial<QueueSnapshot>
  return {
    queues: snapshot.queues ?? { HIGH: [], MEDIUM: [], LOW: [] },
    workers: snapshot.workers ?? { available: 0, active: 0, total: 0 },
    workerManager: snapshot.workerManager ?? [],
    resources: snapshot.resources ?? { maxCPU: 80, maxRAM: 70, maxDisk: 85, cpu: 0, ram: 0, disk: 0 },
  }
}

export async function getProcessors(): Promise<ProcessorInfo[]> {
  const res = await fetch('/api/processors')
  if (!res.ok) throw new ApiError(`Processor registry request failed (${res.status})`)
  const data = (await res.json()) as { processors: ProcessorInfo[] }
  return data.processors
}

export async function getJob(id: string): Promise<JobDetail> {
  const res = await fetch(`/api/jobs/${encodeURIComponent(id)}`)
  if (!res.ok) throw new ApiError(`Job request failed (${res.status})`)
  return (await res.json()) as JobDetail
}

export function convertFile(file: File, target: ConvertTarget, signal?: AbortSignal, onJob?: (id: string) => void) {
  const form = new FormData()
  form.append('file', file)
  form.append('target', target)
  return postFile('/api/convert', form, signal, onJob)
}

export function mergeFiles(files: File[], signal?: AbortSignal, onJob?: (id: string) => void) {
  const form = new FormData()
  files.forEach((f) => form.append('files', f))
  return postFile('/api/merge', form, signal, onJob)
}

export function compressFiles(files: File[], quality: CompressQuality, signal?: AbortSignal, onJob?: (id: string) => void) {
  const form = new FormData()
  files.forEach((f) => form.append('files', f))
  form.append('quality', quality)
  return postFile('/api/compress', form, signal, onJob)
}

export function processMedia(file: File, kind: 'image' | 'video', quality: CompressQuality, format?: ImageFormat, signal?: AbortSignal, onJob?: (id: string) => void) {
  const form = new FormData()
  form.append('file', file)
  form.append('kind', kind)
  form.append('quality', quality)
  if (format) form.append('format', format)
  return postFile('/api/media', form, signal, onJob)
}

export async function cancelJob(id: string): Promise<JobDetail> {
  const res = await fetch(`/api/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' })
  if (!res.ok) throw new ApiError(`Cancel request failed (${res.status})`)
  return (await res.json()) as JobDetail
}

export async function getMediaCapabilities(): Promise<MediaCapabilities> {
  const res = await fetch('/api/media/capabilities')
  if (!res.ok) throw new ApiError(`Media capabilities request failed (${res.status})`)
  return (await res.json()) as MediaCapabilities
}

export async function startMediaJob(file: File, kind: 'image' | 'video', quality: CompressQuality, format?: ImageFormat): Promise<JobDetail> {
  const form = new FormData()
  form.append('file', file); form.append('kind', kind); form.append('quality', quality)
  if (format) form.append('format', format)
  const res = await fetch('/api/jobs', { method: 'POST', body: form })
  if (!res.ok) throw new ApiError(`Job request failed (${res.status})`)
  return (await res.json()) as JobDetail
}

export async function downloadJobOutput(id: string): Promise<BlobResult> {
  const res = await fetch(`/api/jobs/${encodeURIComponent(id)}/output`)
  if (!res.ok) throw new ApiError(`Output request failed (${res.status})`)
  return { blob: await res.blob(), filename: res.headers.get('X-Filename') ?? 'download', jobId: id, jobStatus: 'COMPLETED' }
}

export function watchJob(id: string, onUpdate: (job: JobDetail) => void, onError: () => void): () => void {
  const source = new EventSource(`/api/jobs/${encodeURIComponent(id)}/events`)
  const handle = (event: MessageEvent<string>) => {
    const data = JSON.parse(event.data) as { job?: JobDetail }
    if (data.job) onUpdate(data.job)
  }
  source.addEventListener('snapshot', handle)
  source.addEventListener('progress', handle)
  source.addEventListener('processing', handle)
  source.addEventListener('validating', handle)
  source.addEventListener('completed', handle)
  source.addEventListener('failed', handle)
  source.addEventListener('cancelled', handle)
  source.onerror = onError
  return () => source.close()
}
