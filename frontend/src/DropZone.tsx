import { File as FileIcon, X } from '@phosphor-icons/react'
import { useId } from 'react'

type Props = {
  files: File[]
  accept: string
  multiple: boolean
  hint: string
  onAdd: (files: File[]) => void
  onRemove: (index: number) => void
  onClear: () => void
}

export function DropZone({
  files,
  accept,
  multiple,
  hint,
  onAdd,
  onRemove,
  onClear,
}: Props) {
  const id = useId()

  return (
    <div className="grid gap-4">
      <label
        htmlFor={id}
        onDragOver={(e) => {
          e.preventDefault()
        }}
        onDrop={(e) => {
          e.preventDefault()
          const next = Array.from(e.dataTransfer.files)
          if (next.length) onAdd(next)
        }}
        className="flex min-h-52 cursor-pointer flex-col items-center justify-center rounded-[10px] border border-dashed border-line bg-white px-6 py-10 text-center transition-colors hover:border-leaf hover:bg-leaf-soft/40 dark:bg-[#1b1f1c]"
      >
        <input
          id={id}
          type="file"
          className="sr-only"
          accept={accept}
          multiple={multiple}
          onChange={(e) => {
            const next = Array.from(e.target.files ?? [])
            if (next.length) onAdd(next)
            e.target.value = ''
          }}
        />
        <span className="text-lg font-medium tracking-tight text-ink">Drop files here</span>
        <span className="mt-2 max-w-[42ch] text-sm leading-relaxed text-muted">{hint}</span>
        <span className="mt-6 inline-flex h-10 items-center rounded-[6px] bg-ink px-4 text-sm font-medium text-[#f3f4f1] active:scale-[0.98]">
          Browse
        </span>
      </label>

      {files.length > 0 && (
        <div>
          <div className="mb-2 flex items-center justify-between">
            <p className="text-sm text-muted">{files.length} selected</p>
            <button
              type="button"
              onClick={onClear}
              className="text-sm text-muted underline-offset-2 hover:text-ink hover:underline"
            >
              Clear
            </button>
          </div>
          <ul className="grid gap-2">
            {files.map((file, i) => (
              <li
                key={`${file.name}-${file.size}-${i}`}
                className="flex items-center gap-3 rounded-[10px] border border-line bg-white px-3 py-2.5 dark:bg-[#1b1f1c]"
              >
                <FileIcon size={18} weight="bold" className="shrink-0 text-leaf" />
                <span className="min-w-0 flex-1 truncate text-sm">{file.name}</span>
                <span className="shrink-0 font-mono text-xs text-muted">{formatSize(file.size)}</span>
                <button
                  type="button"
                  aria-label={`Remove ${file.name}`}
                  onClick={() => onRemove(i)}
                  className="grid size-8 place-items-center rounded-[6px] text-muted hover:bg-paper hover:text-ink"
                >
                  <X size={16} weight="bold" />
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

function formatSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}
