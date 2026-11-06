/**
 * A site bitploy serves itself (plain website files, or a front end it builds
 * to files): none of its code runs on the server, so a variable only reaches
 * it through the build.
 */
export function isServedSite(a: { static?: boolean; build_mode?: string } | null | undefined): boolean {
  return !!a && (!!a.static || a.build_mode === 'static' || a.build_mode === 'static_build')
}

/**
 * A change to the variable reaches the app only with a new build (a deploy),
 * not a restart: the live version's build read it (build_reads, "*" for
 * every variable), or the app is a site bitploy serves. Mirrors
 * App.VariableNeedsDeploy in internal/app/types.go.
 */
export function variableNeedsDeploy(
  a: { static?: boolean; build_mode?: string; current_deployment?: { build_reads?: string[] } | null } | null | undefined,
  name: string,
): boolean {
  if (!a) return false
  if (isServedSite(a)) return true
  const reads = a.current_deployment?.build_reads ?? []
  return reads.includes('*') || reads.includes(name)
}
