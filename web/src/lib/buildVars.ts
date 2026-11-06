import type { EnvBuild, EnvVar } from './types'

/**
 * Whether the image build gets a variable too (set_env build=). "none": only
 * the running app gets it, like every variable before build variables existed.
 */
export type BuildChoice = 'none' | EnvBuild

export const BUILD_CHOICES: { value: BuildChoice; label: string; help: string }[] = [
  { value: 'none', label: 'Only when it runs', help: 'The running app reads it. The build does not see it.' },
  {
    value: 'public',
    label: 'Also when it builds, public',
    help: 'Also given to the build, so a site built with Vite, Next.js or Astro can put it in its pages. Anyone can read it in the site, so never use it for a password or a private key. With your own Dockerfile, add ARG and the name before the step that needs it.',
  },
  {
    value: 'secret',
    label: 'Also when it builds, secret',
    help: 'Also given to the build as a secret, for a key only the build needs. It never ends up in the app. With your own Dockerfile, read it in a step with RUN --mount=type=secret,id=NAME,env=NAME.',
  },
]

export function buildChoiceOf(v: Pick<EnvVar, 'build'>): BuildChoice {
  return v.build === 'public' || v.build === 'secret' ? v.build : 'none'
}

export function buildChoice(value: BuildChoice) {
  return BUILD_CHOICES.find((c) => c.value === value) ?? BUILD_CHOICES[0]!
}

// Prefixes front-end tools read while they build and bake into the site.
const PUBLIC_PREFIXES = ['VITE_', 'NEXT_PUBLIC_', 'PUBLIC_', 'REACT_APP_', 'NUXT_PUBLIC_', 'GATSBY_', 'EXPO_PUBLIC_', 'VUE_APP_']

/** A name that a front end reads while it builds (VITE_API_URL, NEXT_PUBLIC_KEY). */
export function looksBuildTime(name: string): boolean {
  const n = name.trim().toUpperCase()
  return PUBLIC_PREFIXES.some((p) => n.startsWith(p) && n.length > p.length)
}

/** The short tag a variable gets in the list, or '' for one only the running app sees. */
export function buildTag(v: Pick<EnvVar, 'build'>): string {
  switch (buildChoiceOf(v)) {
    case 'public':
      return 'build, public'
    case 'secret':
      return 'build, secret'
  }
  return ''
}

/**
 * A site bitploy serves itself (plain website files, or a front end it builds
 * to files): none of its code runs on the server, so a variable only reaches
 * it through the build.
 */
export function isServedSite(a: { static?: boolean; build_mode?: string } | null | undefined): boolean {
  return !!a && (!!a.static || a.build_mode === 'static' || a.build_mode === 'static_build')
}
