// Lightweight client-side CSV helpers used by the traffic pages to export the
// data already loaded in the browser (no extra round-trip). For large server-
// side datasets the egress page streams a CSV straight from the backend instead
// (see downloadEgressCSV in api/traffic.ts).

export interface CsvColumn<T> {
  header: string
  value: (row: T) => string | number | null | undefined
}

// Quote a single field per RFC 4180: wrap in double quotes when it contains a
// comma, quote, or newline, and double any embedded quotes.
function escapeField(v: unknown): string {
  let s = v === null || v === undefined ? '' : String(v)
  if (/[",\n\r]/.test(s)) {
    s = '"' + s.replace(/"/g, '""') + '"'
  }
  return s
}

// Build a CSV string from rows and an ordered column spec.
export function buildCsv<T>(rows: T[], columns: CsvColumn<T>[]): string {
  const head = columns.map((c) => escapeField(c.header)).join(',')
  const body = rows.map((r) => columns.map((c) => escapeField(c.value(r))).join(',')).join('\r\n')
  return body ? head + '\r\n' + body : head
}

// Hand a Blob to the browser as a file download.
export function triggerBlobDownload(filename: string, blob: Blob): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

// Download text as a UTF-8 file. A leading BOM is prepended so spreadsheet apps
// (notably Excel) render CJK characters correctly instead of mojibake.
export function downloadTextFile(filename: string, content: string, mime = 'text/csv;charset=utf-8'): void {
  const bom = String.fromCharCode(0xfeff)
  triggerBlobDownload(filename, new Blob([bom + content], { type: mime }))
}

// Compact local-time stamp for filenames, e.g. 20260611-1530.
export function fileStamp(d: Date = new Date()): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}`
}
