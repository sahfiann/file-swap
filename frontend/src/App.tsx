import { useEffect, useRef, useState } from 'react'
import { cancelJob, compressFiles, convertFile, downloadJobOutput, getJob, getMediaCapabilities, getProcessors, getQueue, mergeFiles, processMedia, startMediaJob, watchJob, type AnalysisReport, type ConvertTarget, type ImageFormat, type JobDetail, type MediaCapabilities, type ProcessorInfo, type QueueSnapshot, type Tool } from './api'

type Phase = 'desktop' | 'config' | 'processing' | 'complete'

const tools: { name: string; kind: string; tool: Tool; art: string; target?: ConvertTarget }[] = [
  { name: 'PDF_to_DOCX.exe', kind: 'PDF', tool: 'convert', target: 'docx', art: 'document pdf-document' },
  { name: 'DOCX_to_PDF.exe', kind: 'DOC', tool: 'convert', target: 'pdf', art: 'document docx-document' },
  { name: 'Merge_Files.exe', kind: 'MRG', tool: 'merge', art: 'folder' },
  { name: 'Image_Optimize.exe', kind: 'IMG', tool: 'media', art: 'scan' },
  { name: 'Video_Process.sys', kind: 'VID', tool: 'media', art: 'chip' },
]

const terminalLines = [
  '> Worker pool connection established...',
  '> Secure payload accepted...',
  '> Piper protocol executing...',
  '> Output channel prepared...',
]

function progressBar(value: number, width = 20) {
  const filled = Math.round((Math.max(0, Math.min(100, value)) / 100) * width)
  return `${'█'.repeat(filled)}${'░'.repeat(width - filled)} ${value.toFixed(2)}%`
}

export default function App() {
  const [phase, setPhase] = useState<Phase>('desktop')
  const [menuOpen, setMenuOpen] = useState<string | null>(null)
  const [showConsole, setShowConsole] = useState(true)
  const [showAbout, setShowAbout] = useState(false)
  const [files, setFiles] = useState<File[]>([])
  const [tool, setTool] = useState<Tool>('convert')
  const [target, setTarget] = useState<ConvertTarget>('docx')
  const [mediaKind, setMediaKind] = useState<'image' | 'video'>('image')
  const [imageFormat, setImageFormat] = useState<ImageFormat>('webp')
  const [compression, setCompression] = useState('max')
  const [stripMetadata, setStripMetadata] = useState(true)
  const [download, setDownload] = useState<{ url: string; name: string } | null>(null)
  const [resultStats, setResultStats] = useState<{ inputBytes: number; outputBytes: number } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [terminalLog, setTerminalLog] = useState<string[]>([])
  const [progress, setProgress] = useState(0)
  const [jobId, setJobId] = useState<string | null>(null)
  const [queue, setQueue] = useState<QueueSnapshot | null>(null)
  const [processorRegistry, setProcessorRegistry] = useState<ProcessorInfo[]>([])
  const [analysis, setAnalysis] = useState<AnalysisReport | null>(null)
  const [videoStats, setVideoStats] = useState<JobDetail | null>(null)
  const [mediaCapabilities, setMediaCapabilities] = useState<MediaCapabilities | null>(null)
  const stopJobWatch = useRef<(() => void) | null>(null)
  const input = useRef<HTMLInputElement>(null)
  const abortController = useRef<AbortController | null>(null)
  const phaseRef = useRef(phase)
  const queueRef = useRef(queue)
  const [queueError, setQueueError] = useState<string | null>(null)

  useEffect(() => {
    phaseRef.current = phase
    queueRef.current = queue
  }, [phase, queue])

  useEffect(() => {
    let mounted = true
    let timer: number | undefined
    let requestInFlight = false
    let failureCount = 0
    const refresh = async () => {
      if (!mounted || document.visibilityState !== 'visible' || requestInFlight) return
      requestInFlight = true
      try {
        const snapshot = await getQueue()
        if (mounted) {
          setQueue(snapshot)
          setQueueError(null)
          failureCount = 0
        }
      } catch {
        failureCount = Math.min(failureCount + 1, 5)
        if (mounted) setQueueError(failureCount >= 3 ? 'PIPER ENGINE OFFLINE. Queue status will retry automatically.' : 'PIPER ENGINE TEMPORARILY BUSY. Retrying queue status.')
      } finally {
        requestInFlight = false
      }
    }
    const schedule = (immediate = false) => {
      if (!mounted) return
      if (timer !== undefined) window.clearTimeout(timer)
      const hasActiveWork = phaseRef.current === 'processing'
        || Boolean(queueRef.current && (queueRef.current.workers.active > 0
          || queueRef.current.queues.HIGH.length
          || queueRef.current.queues.MEDIUM.length
          || queueRef.current.queues.LOW.length))
      const backoff = failureCount > 0 ? Math.min(30000, 1000 * (2 ** (failureCount - 1))) : hasActiveWork ? 1000 : 5000
      const delay = immediate ? 0 : backoff
      timer = window.setTimeout(async () => {
        await refresh()
        schedule()
      }, delay)
    }
    const onVisibilityChange = () => schedule(document.visibilityState === 'visible')
    void refresh()
    schedule()
    void getProcessors().then(setProcessorRegistry).catch(() => {})
    void getMediaCapabilities().then(setMediaCapabilities).catch(() => {})
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => {
      mounted = false
      if (timer !== undefined) window.clearTimeout(timer)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [])

  const file = files[0]
  const fileName = file?.name ?? 'annual_report.pdf'
  const fileSize = file ? formatSize(file.size) : '12.00 MB'

  function sanitizeImageFormat(fileName: string | undefined, currentFormat: ImageFormat): ImageFormat {
    const lowerName = fileName?.toLowerCase() ?? ''
    if (lowerName.endsWith('.svg')) return currentFormat === 'svg' ? 'svg' : currentFormat
    return currentFormat === 'svg' ? 'webp' : currentFormat
  }

  function selectFiles(next: File[]) {
    if (!next.length) return
    if (download) URL.revokeObjectURL(download.url)
    setFiles(next)
    if (tool === 'convert' && !next[0].name.toLowerCase().endsWith('.pdf')) setTarget('pdf')
    if (tool === 'media' && mediaKind === 'image') setImageFormat(sanitizeImageFormat(next[0].name, imageFormat))
    setDownload(null)
    setResultStats(null)
    setError(null)
    setJobId(null)
    setAnalysis(null)
    setVideoStats(null)
    stopJobWatch.current?.()
    setPhase('config')
  }

  async function execute() {
    if (abortController.current || phase === 'processing') return
    const controller = new AbortController()
    abortController.current = controller
    setPhase('processing')
    setError(null)
    setProgress(tool === 'media' && mediaKind === 'video' ? 0 : 8)
    setTerminalLog([
      `> ${files.length || 1} payload${files.length === 1 ? '' : 's'} accepted...`,
      '> Job created: CREATED',
      '> Analyze: ANALYZING',
      '> Plan: PLANNED',
      '> Queue: QUEUED',
    ])
    try {
      if (!file) {
        await new Promise((resolve) => window.setTimeout(resolve, 2100))
          setProgress(100)
          setPhase('complete')
          return
      }
      if (tool === 'merge' && files.length < 2) throw new Error('Merge Files requires at least two PDF, DOCX, or TXT files.')
      setProgress(tool === 'media' && mediaKind === 'video' ? 0 : 22)
      setTerminalLog((lines) => [...lines, `> ${toolLabel(tool, mediaKind, target)} worker started...`])
      let output
      if (tool === 'media' && mediaKind === 'video') {
        const started = await startMediaJob(file, 'video', compression === 'fast' ? 'fast' : compression === 'max' ? 'high' : 'medium')
        setJobId(started.jobId); setVideoStats(started)
        setTerminalLog((lines) => [...lines, '> FFmpeg initialized', `> Input: ${file.name}`, '> Encoding...'])
        await new Promise<void>((resolve, reject) => {
          stopJobWatch.current = watchJob(started.jobId, (updated) => {
            setVideoStats(updated)
            setProgress(updated.progress)
            if (updated.frame) setTerminalLog((lines) => lines.some((line) => line.includes(`Frame: ${updated.frame}`)) ? lines : [...lines, `> Frame: ${updated.frame} / ${updated.totalFrames ?? '?'}`, `> Speed: ${(updated.speed ?? 0).toFixed(2)}x`, `> ETA: ${updated.remaining ?? 'calculating...'}`])
            if (updated.status === 'COMPLETED') { stopJobWatch.current?.(); resolve() }
            if (updated.status === 'FAILED' || updated.status === 'CANCELLED') { stopJobWatch.current?.(); reject(new Error(updated.error ?? `Job ${updated.status.toLowerCase()}`)) }
          }, () => {})
        })
        output = await downloadJobOutput(started.jobId)
      } else output = tool === 'convert'
          ? await convertFile(file, target, controller.signal, setJobId)
          : tool === 'media'
            ? await processMedia(file, mediaKind, compression === 'max' ? 'high' : 'medium', imageFormat, controller.signal, setJobId)
          : tool === 'compress'
            ? await compressFiles(files, compression === 'max' ? 'high' : 'medium', controller.signal, setJobId)
          : await mergeFiles(files, controller.signal, setJobId)
      if (!(tool === 'media' && mediaKind === 'video')) setProgress(86)
      if (output.jobId) setJobId(output.jobId)
      if (output.jobId) void getJob(output.jobId).then((detail) => setAnalysis(detail.analysis)).catch(() => {})
      setTerminalLog((lines) => [...lines, `> Worker: PROCESSING`, '> Validate: VALIDATING', `> Job ${output.jobStatus ?? 'COMPLETED'}: output ready...`])
      const url = URL.createObjectURL(output.blob)
      setDownload({ url, name: output.filename })
      setResultStats({ inputBytes: files.reduce((total, item) => total + item.size, 0), outputBytes: output.blob.size })
      if (!(tool === 'media' && mediaKind === 'video')) setProgress(100)
      setPhase('complete')
      startDownload(url, output.filename)
    } catch (reason) {
      if (reason instanceof DOMException && reason.name === 'AbortError') {
          setTerminalLog((lines) => [...lines, '> Job cancelled by user.'])
          setProgress(0)
          setPhase('config')
          return
      }
      setError(reason instanceof Error ? reason.message : 'Connection to worker pool was interrupted.')
      setPhase('config')
    } finally {
      if (abortController.current === controller) abortController.current = null
    }
  }

  async function cancelProcessing() {
    if (!jobId) {
      setError('Job ID is not available yet. Please try again.')
      return
    }
    try {
      const cancelled = await cancelJob(jobId)
      stopJobWatch.current?.()
      abortController.current?.abort()
      setTerminalLog((lines) => [...lines, `> Job ${cancelled.jobId} cancelled.`, '> Child process terminated.', '> Temporary files cleaned.'])
      setProgress(0)
      setPhase('config')
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not cancel the job.')
    }
  }

  function chooseTool(next: (typeof tools)[number]) {
    setTool(next.tool)
    if (next.target) setTarget(next.target)
    if (next.name.startsWith('Image')) setMediaKind('image')
    if (next.name.startsWith('Video')) setMediaKind('video')
    if (input.current) input.current.click()
  }

  function clearQueue() {
    releaseDownload()
    setFiles([])
    setResultStats(null)
    setError(null)
    setJobId(null)
    setAnalysis(null)
    setPhase('desktop')
  }

  function releaseDownload() {
    if (download) URL.revokeObjectURL(download.url)
    setDownload(null)
  }

  function openConfiguration() {
    setMenuOpen(null)
    if (file) setPhase('config')
    else input.current?.click()
  }

  return (
    <main className="desktop-shell">
      <section className="win95-window" aria-label="Piper Universal Processing Engine">
        <header className="title-bar">
          <div className="title-bar__mark">P</div>
          <strong>Piper Universal Processing Engine v2.0</strong>
          <button className="window-close" aria-label="Close application">×</button>
        </header>
        <nav className="menu-bar" aria-label="Application menu">
          <div className="menu-item"><button onClick={() => setMenuOpen(menuOpen === 'file' ? null : 'file')}>File</button>{menuOpen === 'file' && <div className="menu-dropdown"><button onClick={() => { setMenuOpen(null); input.current?.click() }}>Open...<kbd>Ctrl+O</kbd></button><button disabled={!file} onClick={openConfiguration}>Properties<kbd>Alt+Enter</kbd></button><hr/><button disabled={!file} onClick={() => { clearQueue(); setMenuOpen(null) }}>Clear Queue</button></div>}</div>
          <div className="menu-item"><button onClick={() => setMenuOpen(menuOpen === 'edit' ? null : 'edit')}>Edit</button>{menuOpen === 'edit' && <div className="menu-dropdown"><button disabled={!file} onClick={() => { clearQueue(); setMenuOpen(null) }}>Remove Staged File<kbd>Del</kbd></button><button onClick={() => { setMenuOpen(null); input.current?.click() }}>Add Files...</button></div>}</div>
          <div className="menu-item"><button onClick={() => setMenuOpen(menuOpen === 'view' ? null : 'view')}>View</button>{menuOpen === 'view' && <div className="menu-dropdown"><button onClick={() => { setShowConsole(!showConsole); setMenuOpen(null) }}>{showConsole ? 'Hide' : 'Show'} System Console</button><button onClick={() => { setMenuOpen(null); window.scrollTo({ top: 0, behavior: 'smooth' }) }}>Desktop Hub</button></div>}</div>
          <div className="menu-item"><button onClick={() => setMenuOpen(menuOpen === 'tools' ? null : 'tools')}>Tools</button>{menuOpen === 'tools' && <div className="menu-dropdown"><button onClick={openConfiguration}>Processing Configuration...</button><hr/>{tools.map((item) => <button key={item.name} onClick={() => { setMenuOpen(null); chooseTool(item) }}>{item.name}</button>)}</div>}</div>
          <div className="menu-item"><button onClick={() => setMenuOpen(menuOpen === 'help' ? null : 'help')}>Help</button>{menuOpen === 'help' && <div className="menu-dropdown right"><button onClick={() => { setMenuOpen(null); setShowAbout(true) }}>About Piper...</button><button onClick={() => { setMenuOpen(null); setShowAbout(true) }}>Protocol Status</button></div>}</div>
          <span className="menu-status"><i /> SYSTEM ONLINE</span>
        </nav>

        <div className="workspace">
          <aside className="side-rail">
            <div className="piper-badge"><span>P</span><b>PIPER</b><small>universal file processing</small></div>
            <div className="rail-rule" />
            <p className="rail-label">NETWORK</p>
            <p className="rail-node"><i /> WORKER_POOL_04</p>
            <p className="rail-node"><i /> MEDIUM_QUEUE</p>
            <p className="rail-node dim"><i /> ARCHIVE_NODE</p>
          </aside>

          <div className="desktop-content">
            <div className="desktop-heading">
              <div><p>UNIVERSAL PROCESSING PLATFORM</p><h1>Desktop Hub</h1></div>
              <span className="version-stamp">BUILD 1995.20 / STABLE</span>
            </div>

            <label
              className="drop-zone"
              onDragOver={(event) => event.preventDefault()}
              onDrop={(event) => { event.preventDefault(); selectFiles(Array.from(event.dataTransfer.files)) }}
            >
              <input ref={input} type="file" accept=".pdf,.doc,.docx,.txt,.png,.jpg,.jpeg,.webp,.gif,.svg,.mp4,.mov,.mkv,.webm" multiple onChange={(event) => { selectFiles(Array.from(event.target.files ?? [])); event.target.value = '' }} />
              <span className="drop-dots" />
              <span className="drop-icon">⇩</span>
              <strong>Drag &amp; Drop Files Here</strong>
              <em>or click to browse local system</em>
              <span className="drop-note">SECURE PIPELINE · 500 MB PER FILE MAXIMUM</span>
            </label>

            <div className="tool-caption"><span>PROCESSING MODULES</span><b>{files.length ? `${files.length} FILE${files.length > 1 ? 'S' : ''} STAGED` : 'SELECT A MODULE OR DROP A FILE'}</b></div>
            <div className="tool-grid">
              {tools.map((item) => (
                <button className="desktop-icon" key={item.name} onClick={() => chooseTool(item)}>
                  <span className={`pixel-art ${item.art}`}>
                    <b className="shortcut-overlay" aria-hidden="true">↗</b>
                    <i>{item.kind}</i>
                    {(item.art.includes('document')) && <em aria-hidden="true">P</em>}
                  </span>
                  <span>{item.name}</span>
                </button>
              ))}
            </div>

            {showConsole && <section className="console-preview" aria-label="Processing terminal preview">
              <div className="terminal-title"><span>_</span> PIPER TERMINAL // QUEUE_MONITOR</div>
              <div className="terminal-copy">
                {(phase === 'processing' || terminalLog.length > 0) && terminalLog.map((line) => <div key={line}>{line}</div>)}
                <div>&gt; {phase === 'processing' ? `Processing ${progress}%` : phase === 'complete' ? 'Output ready for download' : terminalLog.length ? 'Job finished. Ready for next payload.' : 'Awaiting payload from desktop hub'}<span className="cursor">_</span></div>
                {queue && <div className="queue-summary">HIGH {queue.queues.HIGH.length} · MEDIUM {queue.queues.MEDIUM.length} · LOW {queue.queues.LOW.length} · WORKERS {queue.workers.active}/{queue.workers.total}</div>}
                {queueError && <div className="analyzer-summary">{queueError}</div>}
                {queue?.resources && <div className="analyzer-summary">RESOURCE MANAGER: CPU {queue.resources.cpu}/{queue.resources.maxCPU}% / RAM {queue.resources.ram}/{queue.resources.maxRAM}% / DISK {queue.resources.disk}/{queue.resources.maxDisk}%</div>}
                {analysis && <div className="analyzer-summary">ANALYZER: {analysis.type} / {formatSize(analysis.size)} / {analysis.dimensions || `${analysis.pages ?? 0} pages`} / {analysis.complexity}</div>}
                {analysis && <div className="analyzer-summary">ANALYZER -&gt; SCHEDULER -&gt; {analysis.complexity === 'HIGH' && analysis.type === 'MP4' ? 'HIGH RESOURCE -&gt; MEDIA WORKER' : files.length > 1 && ['JPEG', 'JPG', 'PNG', 'WEBP', 'GIF'].includes(analysis.type) ? 'BATCH IMAGE -&gt; IMAGE WORKER' : `${analysis.type} -&gt; ${analysis.type === 'PDF' ? 'PDF WORKER' : 'PROCESSOR WORKER'}`}</div>}
                {phase === 'processing' && <div className="console-progress"><span style={{ width: `${progress}%` }} /></div>}
              </div>
            </section>}
            {queue && <section className="console-preview queue-monitor" aria-label="Job queue">
              <div className="terminal-title"><span>&gt;</span> MEDIUM_QUEUE // SCHEDULER</div>
              <div className="terminal-copy">
                {(['HIGH', 'MEDIUM', 'LOW'] as const).map((level) => <div key={level}><b>{level}</b>{queue.queues[level].length ? queue.queues[level].map((item) => <div key={item.jobId}>  Job {item.jobId} / {item.schedule ?? item.processor} / {item.worker ?? 'waiting'}</div>) : <div>  Empty</div>}</div>)}
                <div>&gt; Scheduler: priority + processor + quota + worker availability</div>
              </div>
            </section>}
            {queue && <section className="console-preview worker-manager" aria-label="Piper Worker Manager">
              <div className="terminal-title"><span>&gt;</span> PIPER WORKER MANAGER</div>
              <div className="terminal-copy">{queue.workerManager.map((worker) => <div key={worker.category}>{worker.category.padEnd(8, ' ')} {worker.total} workers <span className="worker-availability">({worker.active} active / {worker.available} available)</span></div>)}</div>
            </section>}
            {processorRegistry.length > 0 && <section className="console-preview processor-registry" aria-label="Processor Registry">
              <div className="terminal-title"><span>&gt;</span> PROCESSOR REGISTRY</div>
              <div className="terminal-copy">{processorRegistry.map((item) => <div key={item.name}>{item.name} <span className="worker-availability">[{item.category}]</span></div>)}<div>&gt; Contract: Analyze / CanHandle / Estimate / Process / Validate / Cancel</div></div>
            </section>}
          </div>
        </div>
        <footer className="status-bar"><span>Ready.</span><span>{file ? `Staged: ${file.name}` : 'No files in queue.'}</span><span>UTC +07:00</span></footer>
      </section>

      {phase === 'config' && <section className="modal-layer" aria-modal="true" role="dialog" aria-label="Processing configuration">
        <div className="dialog-window config-dialog">
          <header className="title-bar"><div className="title-bar__mark">P</div><strong>Processing_Configuration.exe</strong><button className="window-close" onClick={() => setPhase('desktop')}>×</button></header>
          <div className="dialog-body">
            <div className="file-summary"><span className="large-file-icon">{tool === 'merge' ? 'MRG' : tool === 'media' ? mediaKind.toUpperCase().slice(0, 3) : target.toUpperCase()}</span><div><strong>{tool === 'merge' ? `${files.length} files staged for merge` : fileName}</strong><code>Size: {fileSize} | Type: {tool === 'merge' ? 'Multi-document batch' : tool === 'media' ? `${mediaKind} media` : 'Document'}</code><code>Path: C:\\Piper\\Incoming\\{fileName}</code></div></div>
            <fieldset className="group-box workflow-box"><legend>Processing Mode</legend>
              <label><input type="radio" checked={tool === 'convert' && target === 'docx'} onChange={() => { setTool('convert'); setTarget('docx') }} name="workflow" /> <span /> PDF to DOCX <small>Editable Word document; layout preserved where source permits</small></label>
              <label><input type="radio" checked={tool === 'convert' && target === 'pdf'} onChange={() => { setTool('convert'); setTarget('pdf') }} name="workflow" /> <span /> DOCX to PDF <small>High-fidelity PDF export with original image resolution</small></label>
              <label><input type="radio" checked={tool === 'merge'} onChange={() => setTool('merge')} name="workflow" /> <span /> Merge Files <small>Combine two or more PDFs, DOCX, or TXT files into one PDF</small></label>
              <label><input type="radio" checked={tool === 'media' && mediaKind === 'image'} onChange={() => { setTool('media'); setMediaKind('image') }} name="workflow" /> <span /> Optimize Image <small>PNG, JPG, WEBP, GIF, or SVG with dimensions retained</small></label>
              <label><input type="radio" checked={tool === 'media' && mediaKind === 'video'} onChange={() => { setTool('media'); setMediaKind('video') }} name="workflow" /> <span /> Process Video <small>MP4, MOV, MKV, or WEBM to high-quality H.264 MP4</small></label>
            </fieldset>
            {tool === 'media' && mediaKind === 'image' && <fieldset className="group-box format-box"><legend>Download Image As</legend>
             <label><input type="radio" checked={imageFormat === 'webp'} onChange={() => setImageFormat('webp')} name="image-format" /> <span /> WebP <small>Best size-to-quality ratio for web delivery</small></label>
             <label><input type="radio" checked={imageFormat === 'jpg'} onChange={() => setImageFormat('jpg')} name="image-format" /> <span /> JPG <small>High-quality lossy photo output</small></label>
             <label><input type="radio" checked={imageFormat === 'png'} onChange={() => setImageFormat('png')} name="image-format" /> <span /> PNG <small>Lossless output for graphics and transparency</small></label>
             <label><input type="radio" checked={imageFormat === 'gif'} onChange={() => setImageFormat('gif')} name="image-format" /> <span /> GIF <small>Compatible palette-based output for simple graphics</small></label>
             <label className={file?.name.toLowerCase().endsWith('.svg') ? '' : 'option-disabled'}><input type="radio" disabled={!file?.name.toLowerCase().endsWith('.svg')} checked={imageFormat === 'svg'} onChange={() => setImageFormat('svg')} name="image-format" /> <span /> SVG <small>Vector-preserving output, available for SVG sources only</small></label>
            </fieldset>}
            <fieldset className="group-box"><legend>{tool === 'media' && mediaKind === 'video' ? 'Video Encoding Profile' : 'Optimization Engine (Pied Piper Protocol)'}</legend>
              {tool === 'media' && mediaKind === 'video' && <><label><input type="radio" checked={compression === 'fast'} onChange={() => setCompression('fast')} name="compression" /> <span /> FAST <small>1.5M bitrate, prioritizes shorter processing time</small></label><label><input type="radio" checked={compression === 'standard'} onChange={() => setCompression('standard')} name="compression" /> <span /> MEDIUM <small>2M bitrate, balanced size and visual quality</small></label><label><input type="radio" checked={compression === 'max'} onChange={() => setCompression('max')} name="compression" /> <span /> HIGH <small>4M bitrate, prioritizes detail retention</small></label>{mediaCapabilities && <small className="capability-note">Encoder: {mediaCapabilities.encoder}{mediaCapabilities.hardware ? ' (hardware acceleration verified)' : ' (CPU fallback)'}</small>}</>}
              {!(tool === 'media' && mediaKind === 'video') && <><label><input type="radio" checked={compression === 'max'} onChange={() => setCompression('max')} name="compression" /> <span /> High Quality <small>Recommended: preserve detail and original dimensions</small></label>
              <label><input type="radio" checked={compression === 'standard'} onChange={() => setCompression('standard')} name="compression" /> <span /> Balanced Output <small>Smaller output with very good visual quality</small></label>
              </>}
              <label><input type="checkbox" checked={stripMetadata} onChange={(e) => setStripMetadata(e.target.checked)} /> <span /> Strip Metadata <small>Remove embedded authoring data</small></label>
            </fieldset>
            {error && <p className="dialog-error">{error}</p>}
            <div className="dialog-actions"><button className="win-button" onClick={() => setPhase('desktop')}>Cancel</button><button className="win-button primary" onClick={() => void execute()}><b>P</b> {tool === 'merge' ? 'Execute_Merge' : tool === 'media' ? 'Execute_Media' : 'Execute_Conversion'}</button></div>
          </div>
        </div>
      </section>}

      {phase === 'processing' && <section className="modal-layer" aria-modal="true" role="dialog" aria-label="Processing file">
        <div className="dialog-window terminal-window">
          <header className="title-bar"><div className="title-bar__mark">&gt;</div><strong>Piper_Terminal.exe - active session</strong></header>
          <div className="terminal-body">{terminalLines.map((line, index) => <p key={line} style={{ animationDelay: `${index * .18}s` }}>{line}</p>)}{terminalLog.map((line) => <p key={line} className="terminal-live">{line}</p>)}{jobId && <p className="terminal-live">&gt; Job ID: {jobId}</p>}{videoStats && <><div className="video-metrics"><strong>Processing Video</strong><code>{progressBar(progress)}</code><b>Frame {videoStats.frame != null ? videoStats.frame : '--'} / {videoStats.totalFrames != null ? videoStats.totalFrames : '--'}</b><span>FPS {videoStats.fps != null ? videoStats.fps.toFixed(1) : '--'} | Speed {videoStats.speed != null ? `${videoStats.speed.toFixed(2)}x` : '--'}</span><span>Elapsed {videoStats.elapsed || '--'} | Remaining {videoStats.remaining || '--'}</span></div><div className="video-benchmark"><b>VIDEO BENCHMARK</b><div><span>INPUT</span><strong>{formatSize(videoStats.analysis?.size ?? 0)}</strong><small>{videoStats.analysis?.dimensions ?? 'Reading dimensions'} | {videoStats.analysis?.type ?? 'VIDEO'}</small></div><div><span>PROCESSING</span><strong>{compression === 'fast' ? 'FAST' : compression === 'max' ? 'HIGH' : 'MEDIUM'}</strong><small>{mediaCapabilities?.encoder ?? 'libopenh264'} | {videoStats.analysis?.complexity ?? 'Analyzing'}</small></div><div><span>OUTPUT</span><strong>{videoStats.outputBytes ? formatSize(videoStats.outputBytes) : 'In progress'}</strong><small>Measured when encoding completes</small></div></div></>}<p className="terminal-active">&gt; {videoStats ? `Progress: ${progress.toFixed(2)}%` : progress < 100 ? `Processing ${progress}%` : 'Finalized'}<span className="cursor">_</span></p><div className="terminal-progress" role="progressbar" aria-valuenow={progress} aria-valuemin={0} aria-valuemax={100}><span style={{ width: `${progress}%` }} /></div></div>
          <div className="terminal-footer"><span>{progress}% COMPLETE</span><span>NETWORK: PIPER_MESH</span><button className="win-button terminal-cancel" onClick={cancelProcessing}>Cancel Job</button></div>
        </div>
      </section>}

      {phase === 'complete' && <section className="modal-layer" aria-modal="true" role="dialog" aria-label="Conversion complete">
        <div className="dialog-window success-dialog">
          <header className="title-bar"><div className="title-bar__mark">P</div><strong>Success</strong><button className="window-close" onClick={() => setPhase('desktop')}>×</button></header>
          <div className="success-body"><span className="info-bubble">i</span><div><h2>Processing Complete.</h2><p>File successfully routed through the Pied Piper Protocol.</p></div></div>
          <div className="result-panel"><div className="result-label">PROCESSING ENGINE / FILE SIZE RESULT</div><div className="size-readout"><span>Original<br/><b>{resultStats ? formatSize(resultStats.inputBytes) : '18.00 MB'}</b></span><i>→</i><span className="green">Result<br/><b>{resultStats ? formatSize(resultStats.outputBytes) : '1.40 MB'}</b></span></div><div className="savings">{resultStats ? formatSizeChange(resultStats.inputBytes, resultStats.outputBytes) : '16.60 MB smaller (92.2%)'}</div><p className="quality-note">QUALITY PROFILE: {qualityProfile(tool, target, mediaKind, compression, imageFormat)}</p>{jobId && <code className="job-id">JOB ID: {jobId}</code>}</div>
          <div className="dialog-actions success-actions"><button className="win-button" onClick={() => { releaseDownload(); setFiles([]); setResultStats(null); setPhase('desktop') }}>Process_Next</button>{download ? <a className="win-button primary download" href={download.url} download={download.name}>↓ Download_Result</a> : <button className="win-button primary" onClick={() => setPhase('desktop')}>↓ Download_Result</button>}</div>
        </div>
      </section>}

      {showAbout && <section className="modal-layer" aria-modal="true" role="dialog" aria-label="About Piper">
        <div className="dialog-window about-dialog"><header className="title-bar"><div className="title-bar__mark">P</div><strong>About Piper</strong><button className="window-close" onClick={() => setShowAbout(false)}>×</button></header><div className="about-body"><div className="about-logo">P</div><div><h2>Piper Universal Processing Engine</h2><p>Version 2.0 · Build 1995.20</p><code>PIED_PIPER_PROTOCOL / ONLINE</code><p className="about-copy">A distributed file-processing system with a very familiar desktop interface.</p></div></div><div className="dialog-actions about-actions"><button className="win-button primary" onClick={() => setShowAbout(false)}>OK</button></div></div>
      </section>}
    </main>
  )
}

function startDownload(url: string, name: string) {
  const link = document.createElement('a')
  link.href = url
  link.download = name
  link.rel = 'noopener'
  link.click()
}

function formatSize(bytes: number) {
  return bytes < 1024 * 1024 ? `${Math.max(1, Math.round(bytes / 1024))} KB` : `${(bytes / (1024 * 1024)).toFixed(2)} MB`
}

function formatSizeChange(inputBytes: number, outputBytes: number) {
  const difference = outputBytes - inputBytes
  if (difference === 0) return 'No file-size change'
  const percent = (Math.abs(difference) / inputBytes) * 100
  return `${formatSize(Math.abs(difference))} ${difference < 0 ? 'smaller' : 'larger'} (${percent.toFixed(1)}%)`
}

function qualityProfile(tool: Tool, target: ConvertTarget, mediaKind: 'image' | 'video', compression: string, imageFormat: ImageFormat) {
  if (tool === 'media' && mediaKind === 'image') return `Original dimensions retained - high-quality ${imageFormat.toUpperCase()} encoding`
  if (tool === 'media' && mediaKind === 'video') return `${compression === 'fast' ? 'FAST' : compression === 'max' ? 'HIGH' : 'MEDIUM'} H.264 profile - original frame size retained`
  if (tool === 'convert' && target === 'pdf') return 'Native Writer PDF export - original image data preserved'
  if (tool === 'convert') return 'High-fidelity page layout with images and text positioning preserved'
  if (tool === 'merge') return 'Source pages combined without page rasterization'
  return compression === 'max' ? 'High-quality compression profile' : 'Balanced compression profile'
}

function toolLabel(tool: Tool, mediaKind: 'image' | 'video', target: ConvertTarget) {
  if (tool === 'convert') return `${target.toUpperCase()} conversion`
  if (tool === 'media') return `${mediaKind} processing`
  return `${tool} operation`
}
