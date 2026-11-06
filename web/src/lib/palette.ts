// Searching the quick switcher (Ctrl K): which items match what is typed, in
// what order, and the groups they are shown in.

export interface PaletteEntry {
  label: string
  /** The heading shown above the group. */
  group: string
  /** Unique per group: two groups can share a heading. */
  groupKey: string
  keywords?: string
  /** In the project you are in: before the same match elsewhere. */
  near?: boolean
}

/** 0: the label starts with the search, 1: a word does, 2: it is in there somewhere; null: no match. */
export function paletteScore(item: PaletteEntry, words: string[]): number | null {
  const label = item.label.toLowerCase()
  const all = `${label} ${item.group.toLowerCase()} ${(item.keywords ?? '').toLowerCase()}`
  if (!words.every((w) => all.includes(w))) return null
  const first = words[0]!
  if (label.startsWith(first)) return 0
  if (label.split(/[\s›/-]+/).some((p) => p.startsWith(first))) return 1
  return 2
}

/**
 * The items that match the search. The group with the best match comes first,
 * groups stay together in their usual order, and within a group the best
 * matches come first, those of the project you are in before the others.
 */
export function paletteMatches<T extends PaletteEntry>(items: readonly T[], query: string, limit = 60): T[] {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (!words.length) return [...items]
  const scored = items.map((item, i) => ({ item, i, s: paletteScore(item, words) })).filter((x): x is { item: T; i: number; s: number } => x.s !== null)
  const rank = new Map<string, number>()
  const first = new Map<string, number>()
  for (const x of scored) {
    rank.set(x.item.groupKey, Math.min(rank.get(x.item.groupKey) ?? 9, x.s))
    if (!first.has(x.item.groupKey)) first.set(x.item.groupKey, x.i)
  }
  scored.sort(
    (a, b) =>
      rank.get(a.item.groupKey)! - rank.get(b.item.groupKey)! ||
      first.get(a.item.groupKey)! - first.get(b.item.groupKey)! ||
      a.s - b.s ||
      Number(!!b.item.near) - Number(!!a.item.near) ||
      a.i - b.i,
  )
  return scored.slice(0, limit).map((x) => x.item)
}

/** Consecutive items of one group under one heading, keyed by the group's key. */
export function paletteGroups<T extends PaletteEntry>(results: readonly T[]): { key: string; group: string; items: { item: T; index: number }[] }[] {
  const out: { key: string; group: string; items: { item: T; index: number }[] }[] = []
  results.forEach((item, index) => {
    const last = out[out.length - 1]
    if (last && last.key === item.groupKey) last.items.push({ item, index })
    else out.push({ key: item.groupKey, group: item.group, items: [{ item, index }] })
  })
  return out
}
