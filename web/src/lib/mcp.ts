import { ref } from 'vue'

// The token just created is kept for the Connect AI page, in this tab's
// sessionStorage only (cleared when the tab closes).
const KEY = 'bitploy.connectToken'

function read(): string {
  try {
    return sessionStorage.getItem(KEY) ?? ''
  } catch {
    return ''
  }
}

export const connectToken = ref<string>(read())

export function rememberToken(value: string) {
  connectToken.value = value
  try {
    if (value) sessionStorage.setItem(KEY, value)
    else sessionStorage.removeItem(KEY)
  } catch {
    // storage unavailable: memory only
  }
}

export const TOKEN_PLACEHOLDER = 'bp_YOUR_TOKEN'

export const STARTER_PROMPT =
  'Use bitploy to put this folder online. Pick a short name, add a Dockerfile if it needs one, and send me the link.'

export interface ClientSnippet {
  id: string
  label: string
  /** Where to put it, in plain words. */
  where: string
  code: string
  note?: string
}

const json = (v: unknown) => JSON.stringify(v, null, 2)

/** Copy-paste setups for the common MCP clients. `mcpUrl` is `<public url>/mcp`. */
export function clientSnippets(mcpUrl: string, token: string): ClientSnippet[] {
  const t = token || TOKEN_PLACEHOLDER
  const bearer = `Bearer ${t}`
  const insecure = mcpUrl.startsWith('http://')
  return [
    {
      id: 'claude-code',
      label: 'Claude Code',
      where: 'Run this once in your terminal. It works in every folder.',
      code: `claude mcp add --scope user --transport http bitploy ${mcpUrl} \\\n  --header "Authorization: ${bearer}"`,
    },
    {
      id: 'claude-desktop',
      label: 'Claude Desktop',
      where: 'Open Settings, then Developer, then Edit Config, and merge this into claude_desktop_config.json. Restart Claude afterwards.',
      code: json({
        mcpServers: {
          bitploy: {
            command: 'npx',
            args: ['-y', 'mcp-remote', mcpUrl, '--header', 'Authorization:${BITPLOY_AUTH}', ...(insecure ? ['--allow-http'] : [])],
            env: { BITPLOY_AUTH: bearer },
          },
        },
      }),
      note: 'Needs Node.js. Claude Desktop connects through the mcp-remote helper.',
    },
    {
      id: 'cursor',
      label: 'Cursor',
      where: 'Save as .cursor/mcp.json in your code folder, or as ~/.cursor/mcp.json to use it in every folder.',
      code: json({ mcpServers: { bitploy: { url: mcpUrl, headers: { Authorization: bearer } } } }),
    },
    {
      id: 'vscode',
      label: 'VS Code',
      where: 'Save as .vscode/mcp.json in your code folder, or run "MCP: Open User Configuration" to use it in every folder.',
      code: json({ servers: { bitploy: { type: 'http', url: mcpUrl, headers: { Authorization: bearer } } } }),
    },
    {
      id: 'other',
      label: 'Other',
      where: 'Most other tools accept this shape. Check the tool for where the file goes.',
      code: json({ mcpServers: { bitploy: { type: 'http', url: mcpUrl, headers: { Authorization: bearer } } } }),
    },
  ]
}
