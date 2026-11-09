import {
  siActualbudget,
  siChangedetection,
  siClickhouse,
  siDirectus,
  siExcalidraw,
  siForgejo,
  siGhost,
  siGrafana,
  siKarakeep,
  siListmonk,
  siMariadb,
  siMealie,
  siMeilisearch,
  siMysql,
  siN8n,
  siNtfy,
  siPocketbase,
  siPostgresql,
  siRedis,
  siSearxng,
  siUmami,
  siUptimekuma,
  siVaultwarden,
  siVikunja,
} from 'simple-icons'
import type { SimpleIcon } from 'simple-icons'

// Brand marks (simple-icons, CC0) bundled with the dashboard, keyed by catalog
// app id or service template id. Ids without a mark fall back to a letter.
const ICONS: Record<string, SimpleIcon> = {
  'actual-budget': siActualbudget,
  changedetection: siChangedetection,
  clickhouse: siClickhouse,
  directus: siDirectus,
  excalidraw: siExcalidraw,
  forgejo: siForgejo,
  ghost: siGhost,
  grafana: siGrafana,
  karakeep: siKarakeep,
  listmonk: siListmonk,
  mariadb: siMariadb,
  mealie: siMealie,
  meilisearch: siMeilisearch,
  mysql: siMysql,
  n8n: siN8n,
  ntfy: siNtfy,
  pgvector: siPostgresql,
  pocketbase: siPocketbase,
  postgres: siPostgresql,
  redis: siRedis,
  searxng: siSearxng,
  umami: siUmami,
  'uptime-kuma': siUptimekuma,
  vaultwarden: siVaultwarden,
  vikunja: siVikunja,
}

export interface Logo {
  path: string
  /** The brand colour, or null to draw it in the text colour. */
  color: string | null
}

// The brand colour only where it reads on both themes and is not blue; marks
// that are blue, near-black or near-white take the text colour.
function brandColor(hex: string): string | null {
  const r = parseInt(hex.slice(0, 2), 16) / 255
  const g = parseInt(hex.slice(2, 4), 16) / 255
  const b = parseInt(hex.slice(4, 6), 16) / 255
  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  const l = (max + min) / 2
  const d = max - min
  let h = 0
  if (d > 0) {
    if (max === r) h = ((g - b) / d + (g < b ? 6 : 0)) * 60
    else if (max === g) h = ((b - r) / d + 2) * 60
    else h = ((r - g) / d + 4) * 60
  }
  const bluish = d > 0.1 && h >= 180 && h <= 290
  if (bluish || d < 0.12 || l < 0.25 || l > 0.8) return null
  return `#${hex}`
}

export function logoFor(id: string): Logo | null {
  const i = ICONS[id]
  return i ? { path: i.path, color: brandColor(i.hex) } : null
}
