import assert from 'node:assert/strict'
import { execFileSync, spawn } from 'node:child_process'
import { createHash, randomUUID } from 'node:crypto'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { basename, join, resolve } from 'node:path'
import { setTimeout as sleep } from 'node:timers/promises'
import { docker, runContainer } from './container.mjs'
import { generateFormula, parseChecksums } from './homebrew.mjs'

const dist = resolve(process.argv[2] ?? 'dist')
const { version } = JSON.parse(readFileSync(join(dist, 'metadata.json'), 'utf8'))
const checksumsText = readFileSync(join(dist, 'checksums.txt'), 'utf8')
const checksums = parseChecksums(checksumsText)
const os = process.platform
const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch]
assert.ok(['darwin', 'linux'].includes(os) && arch, `Unsupported smoke-test platform: ${os}/${process.arch}`)

for (const [filename, hash] of checksums) {
  assert.equal(filename, basename(filename), `Unsafe artifact filename: ${filename}`)
  const actual = createHash('sha256').update(readFileSync(join(dist, filename))).digest('hex')
  assert.equal(actual, hash, `Checksum mismatch: ${filename}`)
}
for (const targetOS of ['darwin', 'linux']) {
  for (const targetArch of ['amd64', 'arm64']) {
    const extensions = targetOS === 'linux' ? ['tar.gz', 'deb', 'rpm'] : ['tar.gz']
    for (const extension of extensions) {
      const filename = `sql-not-so-lite_${version}_${targetOS}_${targetArch}.${extension}`
      assert.ok(checksums.has(filename), `Missing release artifact: ${filename}`)
    }
  }
}
writeFileSync(join(dist, 'sql-not-so-lite.rb'), generateFormula(version, checksumsText))

async function reservePort() {
  const server = createServer()
  await new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', resolve)
  })
  return server
}

async function smoke(binary, home, image) {
  mkdirSync(join(home, '.sql-not-so-lite'), { recursive: true })
  mkdirSync(join(home, 'scan'), { recursive: true })
  const http = await reservePort()
  let grpc
  let httpPort
  let grpcPort
  try {
    grpc = await reservePort()
    httpPort = http.address().port
    grpcPort = grpc.address().port
  } finally {
    await new Promise((resolve, reject) => http.close(error => error ? reject(error) : resolve()))
    if (grpc) await new Promise((resolve, reject) => grpc.close(error => error ? reject(error) : resolve()))
  }
  writeFileSync(join(home, '.sql-not-so-lite', 'config.toml'), `[server]
http_port = ${image ? 9147 : httpPort}
grpc_port = ${image ? 50051 : grpcPort}
`)
  const env = image ? process.env : {
    ...process.env,
    HOME: home,
    SQNSL_DATA_DIR: join(home, 'databases'),
    SQNSL_SCAN_ROOT: join(home, 'scan'),
  }
  const versionOutput = image
    ? runContainer([image, 'version'])
    : execFileSync(binary, ['version'], { env, encoding: 'utf8', timeout: 10_000 })
  assert.equal(versionOutput.trim(), `sql-not-so-lite ${version}`)

  const container = `sqnsl-release-${randomUUID()}`
  const child = image ? null : spawn(binary, ['start'], { env, stdio: ['ignore', 'pipe', 'pipe'] })
  let logs = ''
  let startError
  let created = false
  child?.stdout.on('data', data => { logs += data })
  child?.stderr.on('data', data => { logs += data })
  const exited = child && new Promise(resolve => {
    child.once('error', error => { startError = error; resolve(null) })
    child.once('exit', resolve)
  })
  try {
    if (image) {
      docker([
        'create', '--name', container,
        '--user', `${process.getuid()}:${process.getgid()}`,
        '--mount', `type=bind,source=${home},target=/data`,
        '--publish', `127.0.0.1:${httpPort}:9147`,
        image, 'start',
      ])
      created = true
      docker(['start', container])
    }
    const url = `http://127.0.0.1:${httpPort}`
    let ready = false
    for (let attempt = 0; attempt < 200; attempt++) {
      if (startError) throw startError
      if (child) assert.equal(child.exitCode, null, `Daemon exited before readiness:\n${logs}`)
      try {
        const response = await fetch(`${url}/api/health`, { signal: AbortSignal.timeout(2000) })
        assert.ok(response.ok, `Health endpoint returned ${response.status}`)
        assert.equal((await response.json()).version, version)
        ready = true
        break
      } catch (error) {
        if (!['ECONNREFUSED', 'ECONNRESET', 'UND_ERR_SOCKET'].includes(error.cause?.code)) throw error
        await sleep(100)
      }
    }
    assert.ok(ready, `Daemon did not become ready:\n${logs}`)
    const page = await fetch(url)
    assert.ok(page.ok, `GUI returned ${page.status}`)
    const html = await page.text()
    assert.ok(!html.includes('GUI not built'), 'Release contains the placeholder GUI')
    const asset = html.match(/src="(\/assets\/[^"]+\.js)"/)?.[1]
    assert.ok(asset, 'Release GUI has no compiled JavaScript entry point')
    const javascript = await fetch(`${url}${asset}`)
    assert.ok(javascript.ok, `Embedded GUI asset returned ${javascript.status}`)
    assert.match(javascript.headers.get('content-type') ?? '', /javascript/)
    assert.ok((await javascript.text()).length > 1000, 'Embedded GUI asset is empty')
  } finally {
    if (created) {
      try {
        docker(['stop', '--time', '10', container])
        const status = docker(['wait', container]).stdout.trim()
        const output = docker(['logs', container])
        assert.equal(status, '0', `Container failed to shut down cleanly:\n${output.stdout}${output.stderr}`)
      } finally {
        docker(['rm', '--force', container])
      }
    } else if (child) {
      if (!startError && child.exitCode === null) child.kill('SIGTERM')
      const timeout = setTimeout(() => child.kill('SIGKILL'), 15_000)
      timeout.unref()
      const code = await exited
      clearTimeout(timeout)
      if (!startError) assert.equal(code, 0, `Daemon failed to shut down cleanly:\n${logs}`)
    }
  }
}

const temp = mkdtempSync(join(dist, '.smoke-'))
try {
  execFileSync('tar', ['-xzf', join(dist, `sql-not-so-lite_${version}_${os}_${arch}.tar.gz`), '-C', temp])
  await smoke(join(temp, 'sqnsl'), join(temp, 'native'))
  if (process.argv.includes('--docker')) {
    await smoke(null, join(temp, 'container'), `ghcr.io/mbianchidev/sql-not-so-lite:${version}-${arch}`)
  }
  console.log(`Verified all release checksums/artifacts and native ${os}/${arch} CLI, API, and embedded GUI`)
} finally {
  rmSync(temp, { recursive: true, force: true })
}
