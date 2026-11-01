// Reading a dropped folder and packing it into a zip in the browser, so the
// server gets the same thing as `zip -r app.zip .`. No dependency: the zip
// format needs only a few headers, and the browser deflates with
// CompressionStream. Only erasable TypeScript here, so Node can run the file in a test.

/** What the server accepts (internal/deploy/zip.go DefaultZipLimits). */
export const MAX_ZIP_BYTES = 95 * 1024 * 1024
export const MAX_UNPACKED_BYTES = 500 * 1024 * 1024
export const MAX_FILES = 20_000

export interface PickedFile {
  /** Path inside the project with forward slashes, no leading slash. */
  path: string
  file: File
}

/** Folders and files that never belong in an app, wherever they are. */
const SKIP_DIRS = new Set(['.git', 'node_modules', '__macosx', '.svn', '.hg', '.idea', '.vscode'])
const SKIP_FILES = new Set(['.ds_store', 'thumbs.db', 'desktop.ini', '.env', '.env.local', '.bitploy'])

/** Whether a path is left out: dependency folders, git data, OS leftovers and secrets in .env. */
export function isSkipped(path: string): boolean {
  const parts = path.split('/').filter(Boolean)
  const last = parts.length - 1
  return parts.some((p, i) => SKIP_DIRS.has(p.toLowerCase()) || (i === last && SKIP_FILES.has(p.toLowerCase())))
}

/** The names that were left out, shortest list first, for "Left out: ..." under the drop zone. */
export function skippedLabel(path: string): string {
  const parts = path.split('/').filter(Boolean)
  for (const p of parts) {
    if (SKIP_DIRS.has(p.toLowerCase())) return p
  }
  return parts[parts.length - 1] ?? path
}

export interface Collected {
  files: PickedFile[]
  /** Name of the folder that was dropped (the wrapping folder that was removed), "" for loose files. */
  root: string
  /** Names of what was left out (node_modules, .git, .env ...), without duplicates. */
  skipped: string[]
}

/** The folder that holds every file, if there is exactly one ("my-site" when people drag the folder itself). */
export function wrapperOf(items: { path: string }[]): string {
  if (!items.length) return ''
  const first = items[0]!.path.split('/')[0]!
  return items.every((i) => i.path.includes('/') && i.path.split('/')[0] === first) ? first : ''
}

/** Drops a single wrapping folder, as the server does for a zip. */
export function stripWrapper<T extends { path: string }>(items: T[]): T[] {
  const first = wrapperOf(items)
  return first ? items.map((i) => ({ ...i, path: i.path.slice(first.length + 1) })) : items
}

function finish(all: PickedFile[], skipped: Set<string>): Collected {
  const files: PickedFile[] = []
  for (const f of all) {
    if (isSkipped(f.path)) skipped.add(skippedLabel(f.path))
    else files.push(f)
  }
  return { files: stripWrapper(files), root: wrapperOf(files), skipped: [...skipped] }
}

/** From an <input type="file" webkitdirectory> or a plain multi-file input. */
export function collectFromFileList(list: FileList | File[]): Collected {
  const all: PickedFile[] = []
  for (const file of Array.from(list)) {
    const rel = (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name
    all.push({ path: rel.replace(/\\/g, '/').replace(/^\/+/, ''), file })
  }
  return finish(all, new Set())
}

interface FsEntry {
  isFile: boolean
  isDirectory: boolean
  name: string
  file?: (ok: (f: File) => void, fail: (e: unknown) => void) => void
  createReader?: () => { readEntries: (ok: (e: FsEntry[]) => void, fail: (e: unknown) => void) => void }
}

async function readAllEntries(dir: FsEntry): Promise<FsEntry[]> {
  const reader = dir.createReader!()
  const out: FsEntry[] = []
  // readEntries returns at most ~100 entries per call: ask until it gives none.
  for (;;) {
    const batch = await new Promise<FsEntry[]>((ok, fail) => reader.readEntries(ok, fail))
    if (!batch.length) return out
    out.push(...batch)
  }
}

/** From a drop: folders (with subfolders) and files. Skipped folders are not opened at all. */
export async function collectFromDrop(dt: DataTransfer): Promise<Collected> {
  const entries: FsEntry[] = []
  const loose: File[] = []
  for (const item of Array.from(dt.items ?? [])) {
    if (item.kind !== 'file') continue
    const entry = (item as DataTransferItem & { webkitGetAsEntry?: () => FsEntry | null }).webkitGetAsEntry?.()
    if (entry) entries.push(entry)
    else {
      const f = item.getAsFile()
      if (f) loose.push(f)
    }
  }
  if (!entries.length && !loose.length) return collectFromFileList(dt.files)

  const all: PickedFile[] = []
  const skipped = new Set<string>()
  async function walk(entry: FsEntry, prefix: string): Promise<void> {
    const path = prefix + entry.name
    if (entry.isDirectory) {
      if (SKIP_DIRS.has(entry.name.toLowerCase())) {
        skipped.add(entry.name)
        return
      }
      for (const child of await readAllEntries(entry)) await walk(child, path + '/')
    } else if (entry.isFile) {
      const file = await new Promise<File>((ok, fail) => entry.file!(ok, fail))
      all.push({ path, file })
    }
  }
  for (const e of entries) await walk(e, '')
  for (const f of loose) all.push({ path: f.name, file: f })
  return finish(all, skipped)
}

// ---------------------------------------------------------------------------
// Writing a zip
// ---------------------------------------------------------------------------

let crcTable: Uint32Array | null = null

export function crc32(data: Uint8Array): number {
  if (!crcTable) {
    crcTable = new Uint32Array(256)
    for (let n = 0; n < 256; n++) {
      let c = n
      for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
      crcTable[n] = c >>> 0
    }
  }
  let crc = 0xffffffff
  for (let i = 0; i < data.length; i++) crc = crcTable[(crc ^ data[i]!) & 0xff]! ^ (crc >>> 8)
  return (crc ^ 0xffffffff) >>> 0
}

async function deflateRaw(data: Uint8Array): Promise<Uint8Array | null> {
  if (typeof CompressionStream === 'undefined') return null
  const stream = new Blob([data as BlobPart]).stream().pipeThrough(new CompressionStream('deflate-raw'))
  return new Uint8Array(await new Response(stream).arrayBuffer())
}

function dosDateTime(ms: number): { time: number; date: number } {
  const d = new Date(ms > 0 ? ms : Date.now())
  const year = Math.min(Math.max(d.getFullYear(), 1980), 2107)
  return {
    time: (d.getHours() << 11) | (d.getMinutes() << 5) | (d.getSeconds() >> 1),
    date: ((year - 1980) << 9) | ((d.getMonth() + 1) << 5) | d.getDate(),
  }
}

/**
 * Whether a file is probably meant to be run (the browser does not say): a
 * script starting with #! or a .sh file. Such files get the executable bit, so
 * `RUN ./start.sh` in a Dockerfile works as it does after `zip -r`.
 */
export function isScript(path: string, data: Uint8Array): boolean {
  return /\.sh$/i.test(path) || (data.length > 2 && data[0] === 0x23 && data[1] === 0x21)
}

export interface ZipProgress {
  done: number
  total: number
}

export class ZipTooBigError extends Error {
  readonly kind: 'files' | 'unpacked' | 'zipped'
  readonly bytes: number
  constructor(kind: 'files' | 'unpacked' | 'zipped', bytes: number) {
    super(kind)
    this.name = 'ZipTooBigError'
    this.kind = kind
    this.bytes = bytes
  }
}

/** Checks the sizes the server would refuse, before any work is done. */
export function checkRawLimits(files: PickedFile[]): void {
  if (files.length > MAX_FILES) throw new ZipTooBigError('files', files.length)
  let total = 0
  for (const f of files) total += f.file.size
  if (total > MAX_UNPACKED_BYTES) throw new ZipTooBigError('unpacked', total)
}

/** Packs the files into one zip. Throws ZipTooBigError when it ends up over the server's limit. */
export async function buildZip(files: PickedFile[], onProgress?: (p: ZipProgress) => void, signal?: AbortSignal): Promise<Blob> {
  checkRawLimits(files)
  const enc = new TextEncoder()
  const parts: BlobPart[] = []
  const central: Uint8Array[] = []
  let offset = 0
  let packed = 0

  for (let i = 0; i < files.length; i++) {
    if (signal?.aborted) throw new DOMException('Cancelled', 'AbortError')
    const { path, file } = files[i]!
    const raw = new Uint8Array(await file.arrayBuffer())
    const crc = crc32(raw)
    const deflated = raw.length > 0 ? await deflateRaw(raw) : null
    const useDeflate = deflated !== null && deflated.length < raw.length
    const data = useDeflate ? deflated : raw
    const method = useDeflate ? 8 : 0
    const name = enc.encode(path)
    const { time, date } = dosDateTime(file.lastModified)

    const local = new DataView(new ArrayBuffer(30))
    local.setUint32(0, 0x04034b50, true)
    local.setUint16(4, 20, true)
    local.setUint16(6, 0x0800, true) // names are UTF-8
    local.setUint16(8, method, true)
    local.setUint16(10, time, true)
    local.setUint16(12, date, true)
    local.setUint32(14, crc, true)
    local.setUint32(18, data.length, true)
    local.setUint32(22, raw.length, true)
    local.setUint16(26, name.length, true)
    parts.push(local.buffer, name as BlobPart, data as BlobPart)

    const cd = new DataView(new ArrayBuffer(46))
    cd.setUint32(0, 0x02014b50, true)
    cd.setUint16(4, (3 << 8) | 20, true) // made on Unix, so the file mode below is read
    cd.setUint16(6, 20, true)
    cd.setUint16(8, 0x0800, true)
    cd.setUint16(10, method, true)
    cd.setUint16(12, time, true)
    cd.setUint16(14, date, true)
    cd.setUint32(16, crc, true)
    cd.setUint32(20, data.length, true)
    cd.setUint32(24, raw.length, true)
    cd.setUint16(28, name.length, true)
    cd.setUint32(38, ((isScript(path, raw) ? 0o100755 : 0o100644) << 16) >>> 0, true) // a regular file, rw-r--r-- or rwxr-xr-x
    cd.setUint32(42, offset, true)
    const entry = new Uint8Array(46 + name.length)
    entry.set(new Uint8Array(cd.buffer), 0)
    entry.set(name, 46)
    central.push(entry)

    offset += 30 + name.length + data.length
    packed += data.length
    if (packed > MAX_ZIP_BYTES) throw new ZipTooBigError('zipped', packed)
    onProgress?.({ done: i + 1, total: files.length })
  }

  let cdSize = 0
  for (const c of central) cdSize += c.length
  const end = new DataView(new ArrayBuffer(22))
  end.setUint32(0, 0x06054b50, true)
  end.setUint16(8, central.length, true)
  end.setUint16(10, central.length, true)
  end.setUint32(12, cdSize, true)
  end.setUint32(16, offset, true)
  parts.push(...(central as BlobPart[]), end.buffer)

  const blob = new Blob(parts, { type: 'application/zip' })
  if (blob.size > MAX_ZIP_BYTES) throw new ZipTooBigError('zipped', blob.size)
  return blob
}

// ---------------------------------------------------------------------------
// Reading the names in a zip
// ---------------------------------------------------------------------------

/**
 * The file names inside a zip, read from its table of contents without
 * unpacking anything. Null when the file is not a plain zip this can read
 * (damaged, or zip64): the server still checks it.
 */
export async function listZipNames(file: Blob): Promise<string[] | null> {
  try {
    const tailSize = Math.min(file.size, 22 + 65_535)
    const tail = new DataView(await file.slice(file.size - tailSize).arrayBuffer())
    let eocd = -1
    for (let i = tail.byteLength - 22; i >= 0; i--) {
      if (tail.getUint32(i, true) === 0x06054b50) {
        eocd = i
        break
      }
    }
    if (eocd < 0) return null
    const count = tail.getUint16(eocd + 10, true)
    const size = tail.getUint32(eocd + 12, true)
    const start = tail.getUint32(eocd + 16, true)
    if (count === 0xffff || size === 0xffffffff || start === 0xffffffff) return null
    const dir = new DataView(await file.slice(start, start + size).arrayBuffer())
    const dec = new TextDecoder()
    const names: string[] = []
    let p = 0
    while (p + 46 <= dir.byteLength && dir.getUint32(p, true) === 0x02014b50) {
      const nameLen = dir.getUint16(p + 28, true)
      const extraLen = dir.getUint16(p + 30, true)
      const commentLen = dir.getUint16(p + 32, true)
      names.push(dec.decode(new Uint8Array(dir.buffer, dir.byteOffset + p + 46, nameLen)).replace(/\\/g, '/'))
      p += 46 + nameLen + extraLen + commentLen
    }
    return names.filter((n) => n !== '')
  } catch {
    return null
  }
}

/**
 * The files of a zip as the server lays them out (internal/deploy/zip.go
 * planZip and stripTopFolder): __MACOSX and .DS_Store dropped, and a single
 * top-level folder removed when everything sits inside it. `names` are the
 * entries from listZipNames, folders ending in "/".
 */
export function serverLayout(names: string[]): string[] {
  const entries = names
    .map((n) => ({ dir: n.endsWith('/'), rel: n.replace(/^\/+|\/+$/g, '') }))
    .filter((e) => e.rel !== '' && e.rel.split('/')[0] !== '__MACOSX' && e.rel.split('/').pop() !== '.DS_Store')
  let top = ''
  let nested = false
  let strip = true
  for (const e of entries) {
    const slash = e.rel.indexOf('/')
    if (slash < 0 && !e.dir) {
      strip = false
      break
    }
    const first = slash < 0 ? e.rel : e.rel.slice(0, slash)
    if (top === '') top = first
    else if (top !== first) {
      strip = false
      break
    }
    if (slash >= 0) nested = true
  }
  const files = entries.filter((e) => !e.dir).map((e) => e.rel)
  if (!strip || top === '' || !nested) return files
  return files.filter((f) => f !== top).map((f) => f.slice(top.length + 1))
}
