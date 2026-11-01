import { z } from 'zod'

// The subdomain rules of the server (internal/deploy/slug.go ValidateSlug): 3 to
// 40 characters of a-z, 0-9 and -, none at the start or end, and a few reserved
// words. Whether a name is already taken only the server knows.
const RESERVED = new Set('www app api mcp admin dashboard docs status mail edge custom cdn static assets auth login bitploy traefik'.split(' '))

export const slugSchema = z
  .string()
  .trim()
  .toLowerCase()
  .min(3, 'Use at least 3 characters.')
  .max(40, 'Use at most 40 characters.')
  .regex(/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/, 'Use letters, numbers and -, not at the start or end.')
  .refine((s) => !RESERVED.has(s) && !s.startsWith('bp-'), 'Bitploy keeps this name for itself. Pick another one.')

/** A name as a subdomain: "My cool app!" becomes "my-cool-app". */
export function slugify(name: string): string {
  return name
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 40)
    .replace(/-+$/g, '')
}

/** The app's address from the server's template ("https://{slug}.example.com"). */
export function appAddress(template: string | undefined, slug: string): string {
  return (template || 'https://{slug}.example.com').replace('{slug}', slug || 'your-app')
}
