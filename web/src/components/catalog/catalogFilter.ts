import type { CatalogApp, ServiceTemplate } from '@/lib/types'

/** Words of a search: every one has to be somewhere in the text. */
function hasWords(text: string, query: string): boolean {
  const hay = text.toLowerCase()
  return query
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((w) => hay.includes(w))
}

/** A ready-made app that fits the search and the chosen category ('' is every category). */
export function catalogMatches(c: Pick<CatalogApp, 'name' | 'tagline' | 'category'>, query: string, category: string): boolean {
  return (!category || c.category === category) && hasWords(`${c.name} ${c.tagline} ${c.category}`, query)
}

/** A database or service that fits the search. */
export function templateMatches(t: Pick<ServiceTemplate, 'name' | 'description' | 'category'>, query: string): boolean {
  return hasWords(`${t.name} ${t.description} ${t.category}`, query)
}

/** "databases" reads better as "Database"; the rest just gets a capital. */
export function categoryLabel(c: string): string {
  return c === 'databases' ? 'Database' : c.charAt(0).toUpperCase() + c.slice(1)
}
