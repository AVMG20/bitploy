import { z } from 'zod'

// The subdomain rules of the server (internal/deploy/slug.go ValidateSlug): 1 to
// 40 characters of a-z, 0-9 and -, none at the start or end, and a few reserved
// words. Whether a name is already taken only the server knows.
const RESERVED = new Set('www app api mcp admin dashboard docs status mail edge custom cdn static assets auth login bitploy traefik'.split(' '))

export const slugSchema = z
  .string()
  .trim()
  .toLowerCase()
  .min(1, 'Fill in a subdomain.')
  .max(40, 'Use at most 40 characters.')
  .regex(/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/, 'Use letters, numbers and -, not at the start or end.')
  .refine((s) => !RESERVED.has(s) && !s.startsWith('bp-'), 'bitploy keeps this name for itself. Pick another one.')

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

/**
 * The parts around the name in an app's address on a domain: "https://" and
 * ".example.com". domain "" is the default domain.
 */
export function addressParts(
  me: { apps_domain: string; app_url_template: string; app_domains?: { domain: string; url_template: string }[] } | undefined,
  domain = '',
): { before: string; after: string } {
  const base = me?.app_url_template ?? 'https://{slug}.example.com'
  let template = me?.app_domains?.find((d) => d.domain === domain)?.url_template
  // A domain the person may no longer pick (an app that stays on it): the
  // default address with the domain swapped.
  if (!template && domain && me) template = base.replace(`{slug}.${me.apps_domain}`, `{slug}.${domain}`)
  const [before = 'https://', after = '.example.com'] = (template ?? base).split('{slug}')
  return { before, after }
}

/** The app's address from the server's template ("https://{slug}.example.com"). */
export function appAddress(template: string | undefined, slug: string): string {
  return (template || 'https://{slug}.example.com').replace('{slug}', slug || 'your-app')
}

/** The first address not in taken: portfolio, else portfolio-2, portfolio-3... Within the 40 characters of a subdomain. */
export function freeSlug(base: string, taken: ReadonlySet<string> | readonly string[]): string {
  const used = taken instanceof Set ? taken : new Set(taken)
  if (!base || !used.has(base)) return base
  for (let i = 2; i < 1000; i++) {
    const end = `-${i}`
    const c = `${base.slice(0, 40 - end.length).replace(/-+$/, '')}${end}`
    if (!used.has(c)) return c
  }
  return base
}
