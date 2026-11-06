import assert from 'node:assert/strict'
import { test } from 'node:test'
import { looksLikeCompose, tokenizeDockerfile, tokenizeYaml } from '../src/lib/codeHighlight.ts'

type Tok = { type: string; text: string }
const join = (ts: Tok[]) => ts.map((t) => t.text).join('')
const kinds = (ts: Tok[]) => ts.filter((t) => t.type !== 'text').map((t) => `${t.type}:${t.text}`)
const docker = (s: string) => kinds(tokenizeDockerfile(s))
const yaml = (s: string) => kinds(tokenizeYaml(s))

const DOCKERFILE = `# syntax=docker/dockerfile:1
ARG NODE_VERSION=22
FROM node:\${NODE_VERSION}-alpine AS build
WORKDIR /app
ENV NODE_ENV=production PORT=3000
COPY --chown=node:node package*.json ./
RUN --mount=type=cache,target=/root/.npm \\
    npm ci && \\
    # dropped by Docker

    npm run build
RUN <<EOF
echo "hi $HOME"
EOF
EXPOSE 3000/tcp
HEALTHCHECK --interval=30s CMD wget -qO- http://localhost:$PORT/ || exit 1
CMD ["node", "server.js"]
`

const COMPOSE = `# compose
x-env: &env
  TZ: Europe/Amsterdam
services:
  web:
    image: "ghcr.io/me/app:\${TAG:-latest}"
    <<: *env
    ports: ["3000:3000", 8080]
    environment:
      - DATABASE_URL=postgres://u:\${PG_PASS}@db/app
      DEBUG: false
    command: >
      sh -c "echo $$HOME"
    healthcheck: { test: [CMD, curl, -f, http://localhost], retries: 3 }
  db:
    image: postgres:16 # pinned
---
`

test('the tokens add up to the input, whatever it is', () => {
  const samples = ['', '\n', DOCKERFILE, COMPOSE, 'RUN "unterminated\nFROM x', "key: 'it''s'\n", '<script>alert(1)</script>', 'a: [b, {c: d}]', '\r\nFROM a\r\n', '\t- x: |\n\t  y']
  for (const s of samples) {
    assert.equal(join(tokenizeDockerfile(s)), s, s)
    assert.equal(join(tokenizeYaml(s)), s, s)
  }
})

test('Dockerfile: instructions, flags, variables, strings, comments', () => {
  const k = docker(DOCKERFILE)
  for (const want of [
    'comment:# syntax=docker/dockerfile:1',
    'keyword:ARG',
    'key:NODE_VERSION',
    'keyword:FROM',
    'variable:${NODE_VERSION}',
    'keyword:AS',
    'key:NODE_ENV',
    'key:PORT',
    'key:--chown',
    'key:--mount',
    'operator:&&',
    'comment:    # dropped by Docker',
    'operator:<<EOF',
    'string:echo "hi $HOME"',
    'operator:EOF',
    'number:3000/tcp',
    'key:--interval',
    'variable:$PORT',
    'operator:||',
    'string:"node"',
    'string:"server.js"',
  ])
    assert.ok(k.includes(want), want)
  // HEALTHCHECK ... CMD and the final CMD are both keywords.
  assert.equal(k.filter((x) => x === 'keyword:CMD').length, 2)
})

test('Dockerfile: a continued line does not start an instruction', () => {
  assert.deepEqual(docker('RUN apt-get update \\\n    COPY x'), ['keyword:RUN', 'operator:\\'])
  // Comment and empty lines inside a continuation keep it going.
  assert.deepEqual(docker('RUN a \\\n# note\n\n  FROM b'), ['keyword:RUN', 'operator:\\', 'comment:# note'])
  assert.deepEqual(docker('run echo hi\nonbuild run make'), ['keyword:run', 'keyword:onbuild', 'keyword:run'])
})

test('Dockerfile: the instruction\'s flags are keys, the flags of the command it runs are not', () => {
  assert.deepEqual(docker('RUN --mount=type=cache,target=/root/.npm npm ci --no-audit'), ['keyword:RUN', 'key:--mount', 'punct:='])
  assert.deepEqual(docker('COPY --from=build --chown=node:node /a ./b'), ['keyword:COPY', 'key:--from', 'punct:=', 'key:--chown', 'punct:='])
  assert.deepEqual(docker('RUN a \\\n  --flag'), ['keyword:RUN', 'operator:\\'])
})

test('Dockerfile: variables inside double quotes, not single quotes', () => {
  assert.deepEqual(docker('RUN echo "a $B ${C:-d}" \'$E\''), ['keyword:RUN', 'string:"a ', 'variable:$B', 'string: ', 'variable:${C:-d}', 'string:"', "string:'$E'"])
})

test('Dockerfile: the escape directive', () => {
  assert.deepEqual(docker('# escape=`\nRUN dir `\n  c:\\'), ['comment:# escape=`', 'keyword:RUN', 'operator:`'])
})

test('YAML: keys, values, anchors, comments', () => {
  const k = yaml(COMPOSE)
  for (const want of [
    'comment:# compose',
    'key:x-env',
    'anchor:&env',
    'key:TZ',
    'string:Europe/Amsterdam',
    'key:services',
    'key:image',
    'variable:${TAG:-latest}',
    'key:<<',
    'anchor:*env',
    'string:"3000:3000"',
    'number:8080',
    'punct:-',
    'variable:${PG_PASS}',
    'keyword:false',
    'operator:>',
    'key:test',
    'key:retries',
    'number:3',
    'string:postgres:16',
    'comment:# pinned',
    'punct:---',
  ])
    assert.ok(k.includes(want), want)
})

test('YAML: a block scalar is text up to the next line at its key\'s depth', () => {
  assert.deepEqual(yaml('run: |\n  a: b\n  # not a comment\nnext: 1'), ['key:run', 'punct::', 'operator:|', 'string:  a: b', 'string:  # not a comment', 'key:next', 'punct::', 'number:1'])
})

test('YAML: $$ is a literal dollar, not a variable', () => {
  assert.deepEqual(yaml('cmd: echo $$HOME $USER'), ['key:cmd', 'punct::', 'string:echo $$HOME ', 'variable:$USER'])
})

test('YAML: a URL or a port mapping is a value, not a key', () => {
  assert.deepEqual(yaml('- http://x:80/a'), ['punct:-', 'string:http://x:80/a'])
  assert.deepEqual(yaml('- 8080:80'), ['punct:-', 'string:8080:80'])
  assert.deepEqual(yaml('url: a#b # c'), ['key:url', 'punct::', 'string:a#b', 'comment:# c'])
})

test('a compose file is told apart from a Dockerfile', () => {
  assert.equal(looksLikeCompose(COMPOSE), true)
  assert.equal(looksLikeCompose(DOCKERFILE), false)
})
