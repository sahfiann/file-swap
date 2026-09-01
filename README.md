# File Swap

File Swap is a local-first file processing workspace for converting, merging,
compressing, and optimizing common documents and media. Files are uploaded to
the local Go service, processed on the same machine, and returned directly to
the browser.

## Highlights

- PDF to DOCX conversion with page images and visual layout preserved.
- DOCX, DOC, TXT, PNG, JPG, and JPEG to PDF conversion through LibreOffice.
- Merge multiple PDF, DOCX, and TXT files into a single PDF.
- Compress PDF files with Ghostscript quality profiles.
- Optimize images without resizing and download them as JPG, PNG, WebP, GIF,
  or SVG when the source is already SVG.
- Process MP4, MOV, MKV, and WebM video into H.264 MP4.
- Job Engine lifecycle with explicit creation, analysis, planning, queue,
  worker, processing, validation, completion, failure, and cancellation states.
- Interactive processing monitor with progress state, job logs, Job ID, and
  cancel support.
- Responsive desktop-style interface for desktop, tablet, and smartphone
  screens.
- Maximum upload size of 500 MB per file across all tools.

## Architecture

| Layer | Technology |
| --- | --- |
| Frontend | React 19, TypeScript, Vite, Tailwind CSS |
| Backend | Go 1.22+ and `net/http` |
| Document conversion | LibreOffice (`soffice`) |
| PDF rendering | Poppler (`pdftoppm`) |
| PDF merging | `pdfcpu` |
| PDF compression | Ghostscript (`gs`) |
| Image processing | ImageMagick (`magick`) |
| Video processing | FFmpeg (`ffmpeg`) |

The frontend and backend communicate through the `/api` endpoints. Vite
proxies these requests to the local Go server during development.

### Job Engine

Every operation is represented as a job instead of a direct upload-process-
download action:

```text
UPLOAD -> CREATE JOB -> ANALYZE -> PLAN -> QUEUE -> WORKER
       -> PROCESS -> VALIDATE -> COMPLETE
```

Each job tracks `Job ID`, `User ID`, input file names, operation, processor,
priority, status, progress, timestamps, error details, and output. Supported
states are `CREATED`, `QUEUED`, `ANALYZING`, `PLANNED`, `PROCESSING`,
`VALIDATING`, `COMPLETED`, `FAILED`, and `CANCELLED`.

Completed jobs return `X-Job-ID` and `X-Job-Status` headers. Their metadata can
be inspected with:

```text
GET /api/jobs/{jobId}
```

Video jobs can be submitted asynchronously so the upload request never waits
for FFmpeg:

```text
POST /api/jobs
POST /api/process
GET  /api/jobs/{jobId}/events
GET  /api/jobs/{jobId}/output
```

The events endpoint uses in-memory Server-Sent Events. FFmpeg emits
`frame`, `fps`, `out_time_ms`, and `speed` through its progress pipe; the job
stores the measured frame position, percentage, elapsed time, and ETA. Video
encoding uses the available `libopenh264` encoder with medium/high bitrate
profiles, `-threads 0` for automatic thread selection, and preserves the
source resolution. The runtime selects this encoder because the supported
environment does not provide `libx264`; output size depends on source duration,
motion, and bitrate rather than file size alone.

Cancellation is server-side. Call `POST /api/jobs/{jobId}/cancel` to cancel the
Go job context, terminate the active child process, wait for it to exit, clean
temporary files, and record the final `CANCELLED` state.

The scheduler exposes pending jobs through `GET /api/jobs`, grouped into
`HIGH`, `MEDIUM`, and `LOW` queues. It selects work by priority, processor
availability, resource requirement, and a per-user active-job quota. The Piper
Worker Manager uses fixed worker classes instead of spawning an unrestricted
goroutine per job:

```text
PDF       2 workers
DOCUMENT  2 workers
IMAGE     4 workers
MEDIA     1 worker
OCR       1 worker
```

Jobs are routed to their category queue and can only run when a worker from
that category is available.

The Resource Manager applies `MAX CPU 80%`, `MAX RAM 70%`, and `MAX DISK 85%`
admission limits. Media jobs are also bounded by the single MEDIA worker, so
three simultaneous 500 MB video uploads produce one `PROCESSING` job and two
`QUEUED` jobs instead of starting three encoders.

### Cleanup Engine

Every job follows the retention lifecycle:

```text
UPLOAD -> PROCESS -> RESULT -> DOWNLOAD -> TTL -> DELETE
```

Completed results are retained for 60 minutes. The cleanup engine then removes
the uploaded input directory, generated output directory, and temporary files.
It also scans `/tmp` on startup and every five minutes, deleting stale
`fileswap-*` directories left by abandoned jobs or an interrupted server.

### Processor Registry

Processors use a stable contract so new implementations can be added without
changing existing HTTP handlers:

```text
PDFProcessor
DocumentProcessor
ImageProcessor
VideoProcessor
AudioProcessor
OCRProcessor
ArchiveProcessor
```

Every processor implements `Analyze()`, `CanHandle()`, `Estimate()`,
`Process()`, `Validate()`, and `Cancel()`. The registry is available at
`GET /api/processors` and currently acts as an extension boundary for the
existing conversion and media operations.

### File Analyzer

Before scheduling, the analyzer inspects the first input and attaches a report
to the job. Images include type, size, dimensions, metadata, and complexity.
PDFs include type, size, page count, text presence, image count, encryption, and
complexity. The scheduler receives this report and promotes high-complexity
jobs to high priority when no higher priority was explicitly selected.

The scheduler also records its routing decision and assigned worker. For
example, a large video is routed as `HIGH RESOURCE -> MEDIA WORKER`, while a
multi-image submission is routed as `BATCH IMAGE -> IMAGE WORKER`.

## Prerequisites

Install the following software before running the project:

- Go 1.22 or newer
- Node.js 20 or newer
- npm
- LibreOffice with the `soffice` command
- Poppler with the `pdftoppm` command
- Ghostscript with the `gs` command
- ImageMagick with the `magick` command
- FFmpeg with the `ffmpeg` command

Verify external tools:

```bash
go version
node --version
soffice --version
pdftoppm -v
gs --version
magick --version
ffmpeg -version
```

## Getting Started

Clone the repository and install frontend dependencies:

```bash
git clone <repository-url>
cd file-swap
npm --prefix frontend install
```

Start the backend in one terminal:

```bash
npm run api
```

Start the frontend in another terminal:

```bash
npm run dev
```

Open <http://127.0.0.1:5173>.

The backend listens on <http://127.0.0.1:8080>. Set `PORT` to use another
address or port:

```bash
PORT=9090 npm run api
```

## Available Commands

Run these commands from the repository root:

| Command | Description |
| --- | --- |
| `npm run api` | Start the Go API server |
| `npm run dev` | Start the Vite development server |
| `npm run build` | Create a production frontend build |
| `npm run lint` | Run the frontend linter |
| `npm run preview` | Preview the production frontend build |

Backend checks:

```bash
cd backend
go test ./...
go build ./cmd/server
```

## Supported Operations

### Document conversion

- PDF to DOCX
- DOC to PDF
- DOCX to PDF
- TXT to PDF
- TXT to DOCX
- PNG, JPG, and JPEG to PDF

PDF to DOCX prioritizes visual fidelity. Each source page is rendered at high
resolution and embedded as a page image, preserving the original images,
tables, typography, and positioning. Text in this output is not independently
editable as native Word text.

### Merge

Merge at least two PDF, DOCX, or TXT files. Non-PDF inputs are first converted
to PDF and then combined.

### Compression

Compression accepts one or more PDF files. A single PDF is returned as a PDF;
multiple PDFs are packaged as a ZIP archive.

Quality profiles are `low`, `medium`, and `high`.

### Media processing

Image optimization supports PNG, JPG, JPEG, WebP, GIF, and SVG inputs. Image
dimensions are retained. Raster images can be exported as JPG, PNG, WebP, or
GIF. SVG output is available only for SVG sources so vector data is not
silently replaced by a raster approximation.

Video processing accepts MP4, MOV, MKV, and WebM inputs and returns an H.264
MP4 file while retaining the source frame size.

## API Reference

All processing endpoints return the generated file as an attachment. Results
include `X-Filename`, `X-Original-Bytes`, and `X-Result-Bytes` response headers.

| Method | Endpoint | Fields |
| --- | --- | --- |
| `GET` | `/api/health` | None |
| `POST` | `/api/convert` | `file`, `target`: `pdf` or `docx` |
| `POST` | `/api/merge` | `files` (two or more) |
| `POST` | `/api/compress` | `files`, `quality`: `low`, `medium`, or `high` |
| `POST` | `/api/media` | `file`, `kind`, `quality`, optional image `format` |

Media values:

- `kind`: `image` or `video`
- `quality`: `medium` or `high`
- Image `format`: `jpg`, `png`, `webp`, `gif`, or `svg`

Example health check:

```bash
curl http://127.0.0.1:8080/api/health
```

Example image optimization:

```bash
curl -f \
  -F "file=@photo.png" \
  -F "kind=image" \
  -F "quality=high" \
  -F "format=jpg" \
  -o photo-optimized.jpg \
  http://127.0.0.1:8080/api/media
```

## Upload Limits and Storage

The maximum size is **500 MB per file** for every processing tool. For
multi-file operations, each file may be up to 500 MB. The total request and
temporary working data also depend on available disk space, memory, and server
configuration.

Processing results are stored in temporary directories and streamed back to the
client. Keep enough free disk space for the uploaded files, intermediate
artifacts, and generated output. Do not upload confidential files to a
machine you do not control.

## Troubleshooting

### `could not render PDF pages`

Confirm that Poppler is installed and `pdftoppm` is available in `PATH`.

### `image conversion failed`

Confirm ImageMagick is installed with the required format delegate. Check
support with:

```bash
magick -list format
```

### `video conversion failed`

Confirm FFmpeg is installed and that the input codec is supported.

### PDF to DOCX has no editable text

This is expected for the fidelity-first PDF to DOCX path. The output preserves
the page appearance as embedded images. For editable text, use a dedicated OCR
or PDF structure recovery workflow.

### Upload rejected as too large

The application limit is 500 MB per file. Reverse proxies, web servers, or
hosting providers may impose a smaller request limit and must be configured
separately.

## Security and Privacy

- Files are processed by the local backend and are not intentionally sent to a
  third-party service.
- Uploaded filenames are reduced to safe base names before writing to disk.
- Temporary processing directories are removed after each request.
- File type and per-file size validation happens on the backend.

This project is intended for local or trusted-network use. Add authentication,
HTTPS, rate limiting, and proxy request limits before exposing it publicly.

## Project Layout

```text
backend/
  cmd/server/              HTTP server entrypoint
  internal/httpapi/        API routes and file responses
  internal/convert/        Document conversion
  internal/compress/       PDF compression and ZIP packaging
  internal/media/          Image and video processing
  internal/merge/          PDF merge workflow
  internal/pipeline/       Shared validation and measurements
  internal/files/           Upload validation and temporary file handling
frontend/
  src/App.tsx              Main application interface
  src/api.ts               Frontend API client
  src/index.css            Responsive styling
```

## License

No license is currently specified. Add a license file before distributing the
project or accepting external contributions.

