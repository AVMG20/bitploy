/**
 * The parts of a variable name that tick Hidden by default. MCP set_env uses
 * the same list (secretNameParts in internal/mcp/secretname.go): keep both
 * identical.
 */
const SECRET_NAME_PARTS = ['SECRET', 'TOKEN', 'KEY', 'PASSWORD']

/** A name that holds a secret by the look of it, like STRIPE_SECRET_KEY or github_token. */
export function looksSecretName(name: string): boolean {
  const u = name.toUpperCase()
  return SECRET_NAME_PARTS.some((p) => u.includes(p))
}
