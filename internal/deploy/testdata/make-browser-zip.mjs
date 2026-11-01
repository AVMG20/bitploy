import { buildZip, listZipNames, collectFromFileList } from '../../../web/src/lib/zip.ts'
import { writeFileSync } from 'node:fs'
const mk = (rel, body) => { const f = new File([body], rel.split('/').pop(), { lastModified: 1700000000000 }); Object.defineProperty(f, 'webkitRelativePath', { value: rel }); return f }
const big = 'hello world '.repeat(10000)
const list = [
  mk('my-site/index.html', '<h1>héllo ✓</h1>'),
  mk('my-site/css/site.css', big),
  mk('my-site/empty.txt', ''),
  mk('my-site/start.sh', '#!/bin/sh\necho hi\n'),
  mk('my-site/.env', 'SECRET=1'),
  mk('my-site/node_modules/x/index.js', 'x'),
  mk('my-site/.git/config', 'x'),
  mk('my-site/img/ü ñ.bin', new Uint8Array([0, 1, 2, 3, 250, 251])),
]
const c = collectFromFileList(list)
console.log('paths', c.files.map(f => f.path), 'skipped', c.skipped)
const blob = await buildZip(c.files)
console.log('size', blob.size)
writeFileSync('browser-made.zip', Buffer.from(await blob.arrayBuffer()))
console.log('names', await listZipNames(blob))
