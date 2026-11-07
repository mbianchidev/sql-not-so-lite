import assert from 'node:assert/strict'
import { execFileSync, spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

const source = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const version = '1.2.3'
const entry = '/assets/index.js'
const bundle = '/assets/web.js'
const html = `<script type="module" src="${entry}"></script>
<link rel="modulepreload" crossorigin href="${bundle}">`
const javascript = 'console.log("synthetic GUI");\n'.repeat(100)

function fixture(t, overrides = {}) {
  const temp = mkdtempSync(join(tmpdir(), 'sqnsl-verify-release-test-'))
  t.after(() => rmSync(temp, { recursive: true, force: true }))
  const dist = join(temp, 'dist')
  mkdirSync(dist)
  const routes = {
    '/': { type: 'text/html', body: html },
    [entry]: { type: 'text/javascript', body: 'import "./web.js";\n' },
    [bundle]: { type: 'text/javascript', body: javascript },
    ...overrides,
  }
  writeFileSync(join(temp, 'sqnsl'), `#!/usr/bin/env node
import { readFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { join } from 'node:path'

if (process.argv[2] === 'version') {
  console.log('sql-not-so-lite ${version}')
} else {
  const config = readFileSync(join(process.env.HOME, '.sql-not-so-lite', 'config.toml'), 'utf8')
  const port = Number(config.match(/http_port = (\\d+)/)[1])
  const routes = JSON.parse(process.env.SQNSL_GUI_FIXTURE)
  const server = createServer((request, response) => {
    if (request.url === '/api/health') {
      response.writeHead(200, { 'content-type': 'application/json' })
      response.end(JSON.stringify({ version: '${version}' }))
      return
    }
    const route = routes[request.url]
    response.writeHead(route?.status ?? (route ? 200 : 404), {
      'content-type': route?.type ?? 'text/plain',
    })
    response.end(route?.body ?? 'Missing mock asset')
  })
  server.listen(port, '127.0.0.1')
  process.once('SIGTERM', () => server.close(error => {
    if (error) {
      console.error(error)
      process.exitCode = 1
    }
  }))
}
`, { mode: 0o755 })

  const native = `${process.platform}_${{ x64: 'amd64', arm64: 'arm64' }[process.arch]}`
  const checksums = []
  for (const os of ['darwin', 'linux']) {
    for (const arch of ['amd64', 'arm64']) {
      for (const extension of os === 'linux' ? ['tar.gz', 'deb', 'rpm'] : ['tar.gz']) {
        const filename = `sql-not-so-lite_${version}_${os}_${arch}.${extension}`
        const path = join(dist, filename)
        if (`${os}_${arch}` === native && extension === 'tar.gz') {
          execFileSync('tar', ['-czf', path, '-C', temp, 'sqnsl'])
        } else {
          writeFileSync(path, 'Synthetic release artifact\n')
        }
        const hash = createHash('sha256').update(readFileSync(path)).digest('hex')
        checksums.push(`${hash}  ${filename}`)
      }
    }
  }
  writeFileSync(join(dist, 'metadata.json'), JSON.stringify({ version }))
  writeFileSync(join(dist, 'checksums.txt'), checksums.join('\n'))
  const result = spawnSync(process.execPath, [join(source, 'scripts/verify-release.mjs'), dist], {
    env: { ...process.env, SQNSL_GUI_FIXTURE: JSON.stringify(routes) },
    encoding: 'utf8',
    timeout: 20_000,
  })
  assert.ifError(result.error)
  const output = `${result.stdout}${result.stderr}`
  assert.equal(result.signal, null, output)
  return { code: result.status, output }
}

test('release verification accepts a short JavaScript module loader', t => {
  const result = fixture(t)
  assert.equal(result.code, 0, result.output)
  assert.match(result.output, /Verified all release checksums\/artifacts/)
})

test('release verification rejects an empty linked JavaScript bundle', t => {
  const result = fixture(t, { [bundle]: { type: 'text/javascript', body: '' } })
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /Embedded GUI asset is empty: \/assets\/web\.js/)
})

test('release verification rejects a missing linked JavaScript bundle', t => {
  const result = fixture(t, { [bundle]: null })
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /Embedded GUI asset.*404/)
})

test('release verification rejects HTML served instead of a linked JavaScript bundle', t => {
  const result = fixture(t, { [bundle]: { type: 'text/html', body: html } })
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /Embedded GUI asset has invalid content type: \/assets\/web\.js/)
})

test('release verification still accepts a large JavaScript entry point', t => {
  const result = fixture(t, { [entry]: { type: 'text/javascript', body: javascript } })
  assert.equal(result.code, 0, result.output)
})

for (const body of ['', ' \n\t ']) {
  test(`release verification rejects ${body ? 'whitespace-only' : 'empty'} entry JavaScript`, t => {
    const result = fixture(t, { [entry]: { type: 'text/javascript', body } })
    assert.equal(result.code, 1, result.output)
    assert.match(result.output, /Embedded GUI asset is empty: \/assets\/index\.js/)
  })
}

test('release verification rejects a missing JavaScript entry asset', t => {
  const result = fixture(t, { [entry]: null })
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /Embedded GUI asset.*404/)
})

test('release verification rejects an entry asset with the wrong content type', t => {
  const result = fixture(t, { [entry]: { type: 'text/plain', body: javascript } })
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /Embedded GUI asset has invalid content type: \/assets\/index\.js/)
})

test('release verification rejects the placeholder GUI', t => {
  const result = fixture(t, { '/': { type: 'text/html', body: 'GUI not built' } })
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /Release contains the placeholder GUI/)
})

test('release verification requires a compiled JavaScript entry point', t => {
  const result = fixture(t, { '/': { type: 'text/html', body: '<div>Mock GUI</div>' } })
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /Release GUI has no compiled JavaScript entry point/)
})

test('release verification rejects an unsuccessful GUI response', t => {
  const result = fixture(t, { '/': { status: 503, type: 'text/html', body: html } })
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /GUI returned 503/)
})
