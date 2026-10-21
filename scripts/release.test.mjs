import assert from 'node:assert/strict'
import { execFileSync, spawn } from 'node:child_process'
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

const source = resolve(dirname(fileURLToPath(import.meta.url)), '..')

function fixture(t, options = {}) {
  const temp = mkdtempSync(join(tmpdir(), 'sqnsl-release-test-'))
  t.after(() => rmSync(temp, { recursive: true, force: true }))
  const root = join(temp, 'checkout')
  const remote = join(temp, 'remote.git')
  const bin = join(temp, 'bin')
  mkdirSync(root)
  mkdirSync(bin)
  mkdirSync(join(root, 'scripts'))
  mkdirSync(join(root, 'web'))
  const env = {
    ...Object.fromEntries(Object.entries(process.env).filter(([name]) => !name.startsWith('GIT_'))),
    GIT_CONFIG_NOSYSTEM: '1',
    GIT_CONFIG_GLOBAL: '/dev/null',
    GIT_TERMINAL_PROMPT: '0',
    PATH: `${bin}:${process.env.PATH}`,
    SQNSL_RELEASE_MOCK_STATE: join(temp, 'github.json'),
    SQNSL_RELEASE_TIMEOUT_SECONDS: '8',
    SQNSL_RELEASE_ROLLBACK_TIMEOUT_SECONDS: '3',
    SQNSL_RELEASE_POLL_SECONDS: '0.01',
    SQNSL_RELEASE_COMMAND_TIMEOUT_SECONDS: '3',
  }
  const git = (...args) => execFileSync('git', args, {
    cwd: root, env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
  }).trim()
  git('init', '--bare', '--initial-branch=main', remote)
  git('init', '--initial-branch=main')
  mkdirSync(join(remote, 'hooks'), { recursive: true })
  mkdirSync(join(root, '.git/hooks'), { recursive: true })
  git('config', 'user.name', 'Release Test')
  git('config', 'user.email', 'release-test@example.invalid')
  git('config', 'url.' + `file://${remote}` + '.insteadOf', 'git@github.com:mbianchidev/sql-not-so-lite.git')
  git('remote', 'add', 'origin', 'git@github.com:mbianchidev/sql-not-so-lite.git')
  for (const name of ['.release.sh', 'scripts/release.mjs']) {
    if (existsSync(join(source, name))) copyFileSync(join(source, name), join(root, name))
  }
  if (existsSync(join(root, '.release.sh'))) chmodSync(join(root, '.release.sh'), 0o755)
  copyFileSync(join(source, 'scripts/fixtures/release-gh.mjs'), join(bin, 'gh'))
  chmodSync(join(bin, 'gh'), 0o755)
  writeFileSync(join(root, 'web/package.json'), '{"name":"web","private":true,"version":"0.0.0"}\n')
  writeFileSync(join(root, 'version.go'), 'package fixture\nvar Version = "dev"\n')
  git('add', '.')
  git('commit', '--no-gpg-sign', '-m', 'Fixture main')
  git('tag', '--no-sign', '-a', 'v0.0.1', '-m', 'Previous release')
  git('push', 'origin', 'HEAD:refs/heads/main', 'refs/tags/v0.0.1')
  const commit = git('rev-parse', 'HEAD')
  const tag = options.tag ?? 'v1.2.3'
  const state = { remote, commit, tag, scenario: 'success', calls: [], ...options }
  writeFileSync(env.SQNSL_RELEASE_MOCK_STATE, JSON.stringify(state))
  const readState = () => JSON.parse(readFileSync(env.SQNSL_RELEASE_MOCK_STATE, 'utf8'))
  const journals = () => {
    const dir = join(root, '.git/sqnsl-releases')
    return existsSync(dir) ? readdirSync(dir).filter(name => name.endsWith('.json')).map(name =>
      JSON.parse(readFileSync(join(dir, name), 'utf8')),
    ) : []
  }
  async function release(args = [tag], { signalOn, signal = 'SIGINT', extraEnv = {} } = {}) {
    const child = spawn('bash', ['.release.sh', ...args], { cwd: root, env: { ...env, ...extraEnv } })
    let output = ''
    let signalled = false
    const capture = data => {
      output += data
      if (signalOn && !signalled && output.includes(signalOn)) {
        signalled = true
        child.kill(signal)
      }
    }
    child.stdout.on('data', capture)
    child.stderr.on('data', capture)
    const timeout = setTimeout(() => child.kill('SIGKILL'), 20_000)
    const result = await new Promise((resolve, reject) => {
      child.once('error', reject)
      child.once('close', (code, signal) => resolve({ code, signal, output }))
    }).finally(() => clearTimeout(timeout))
    assert.equal(result.signal, null, result.output)
    return result
  }
  return { root, remote, env, tag, commit, git, release, readState, journals }
}

test('normalizes stable product versions with or without a v prefix', async () => {
  const { normalizeVersion } = await import('./release.mjs')
  assert.equal(normalizeVersion('1.2.3'), 'v1.2.3')
  assert.equal(normalizeVersion('v1.2.3'), 'v1.2.3')
  assert.equal(normalizeVersion('0.0.0'), 'v0.0.0')
  for (const version of ['', '1.2', '01.2.3', 'v01.2.3', 'V1.2.3', 'vv1.2.3',
    '1.2.3-rc.1', '1.2.3+build', ' 1.2.3', '1.2.3\n', '--help', '../1.2.3']) {
    assert.throws(() => normalizeVersion(version), /Expected X.Y.Z or vX.Y.Z/)
  }
})

test('the root release entry point is executable', () => {
  assert.ok(statSync(join(source, '.release.sh')).mode & 0o111)
})

test('publishes a normalized annotated tag on main without bumping unrelated files', async t => {
  const f = fixture(t)
  const before = f.git('rev-parse', 'refs/tags/v0.0.1')
  const result = await f.release(['1.2.3'])
  assert.equal(result.code, 0, result.output)
  assert.equal(f.git('rev-parse', 'v1.2.3^{commit}'), f.commit)
  assert.equal(f.git('cat-file', '-t', 'v1.2.3'), 'tag')
  assert.match(f.git('cat-file', '-p', 'v1.2.3'), /Release-Invocation: [a-f0-9-]+/)
  assert.equal(f.git('rev-parse', 'refs/tags/v0.0.1'), before)
  assert.equal(f.git('status', '--porcelain'), '')
  assert.equal(f.git('rev-parse', 'HEAD'), f.commit)
  assert.equal(JSON.parse(readFileSync(join(f.root, 'web/package.json'), 'utf8')).version, '0.0.0')
  assert.equal(f.journals()[0].outcome, 'released')
  assert.match(result.output, /v1.2.3.*released/)
  assert.ok(f.readState().calls.some(call => call.endpoint.includes('/actions/runs/123')))
  assert.ok(!f.readState().calls.some(call => call.method === 'POST' || call.method === 'DELETE'))
  assert.ok(!existsSync(join(f.root, '.git/sqnsl-release.lock')))
})

test('failed CI rolls back only owned tags after proving publication was skipped', async t => {
  const f = fixture(t, { scenario: 'ci-failure' })
  const previous = f.git('rev-parse', 'refs/tags/v0.0.1')
  const result = await f.release()
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /Rollback complete/)
  assert.equal(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'), '')
  assert.throws(() => f.git('rev-parse', '--verify', 'refs/tags/v1.2.3'))
  assert.equal(f.git('rev-parse', 'refs/tags/v0.0.1'), previous)
  assert.equal(f.git('rev-parse', 'HEAD'), f.commit)
  assert.equal(f.git('status', '--porcelain'), '')
  assert.equal(f.journals()[0].outcome, 'rolled-back')
  assert.ok(f.readState().calls.some(call =>
    call.endpoint === 'repos/mbianchidev/sql-not-so-lite/actions/runs/123/attempts/1/jobs?per_page=100',
  ))
  assert.ok(!f.readState().calls.some(call => call.method !== 'GET'))
})

test('invalid CLI arguments fail before GitHub access or tag creation', async t => {
  const f = fixture(t)
  for (const args of [[], ['1.2.3', 'extra'], ['01.2.3'], ['v1.2.3-rc.1'], ['1.2.3\n']]) {
    const result = await f.release(args)
    assert.equal(result.code, 64, result.output)
    assert.match(result.output, /Usage:/)
  }
  assert.equal(f.readState().calls.length, 0)
  assert.equal(f.journals().length, 0)
})

for (const [name, options, message] of [
  ['existing GitHub release', { existingRelease: true }, /already exists/],
  ['existing GHCR image without a Git tag', { existingImage: true }, /already has.*GHCR/],
  ['existing Homebrew version', { formulaVersion: '1.2.3' }, /already exists.*Homebrew/],
  ['missing package permissions', { noPackageAccess: true }, /read:packages/],
  ['disabled release workflow', { disabled: true }, /workflow is missing or disabled/],
  ['read-only repository access', { readOnly: true }, /Need push access/],
  ['historical run for a deleted tag', { historicalRun: true, scenario: 'ci-failure' }, /historical Release runs/],
]) {
  test(`preflight rejects ${name} without mutating release resources`, async t => {
    const f = fixture(t, options)
    const result = await f.release()
    assert.equal(result.code, 1, result.output)
    assert.match(result.output, message)
    assert.equal(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'), '')
    assert.throws(() => f.git('rev-parse', '--verify', 'refs/tags/v1.2.3'))
    assert.ok(!f.readState().calls.some(call => call.method !== 'GET'))
  })
}

test('preflight rejects existing prefixed and unprefixed Git versions', async t => {
  for (const existing of ['v1.2.3', '1.2.3']) {
    const f = fixture(t)
    f.git('tag', '--no-sign', '-a', existing, '-m', 'Existing release')
    f.git('push', 'origin', `refs/tags/${existing}`)
    const before = f.git('ls-remote', '--refs', 'origin', `refs/tags/${existing}`)
    const result = await f.release()
    assert.equal(result.code, 1, result.output)
    assert.match(result.output, /already exists/)
    assert.equal(f.git('ls-remote', '--refs', 'origin', `refs/tags/${existing}`), before)
    assert.equal(f.readState().calls.length, 0)
  }
})

test('version advancement compares numeric components, not strings or floating point', async t => {
  const older = fixture(t)
  older.git('tag', '--no-sign', '-a', 'v1.10.0', '-m', 'Newer version')
  older.git('push', 'origin', 'refs/tags/v1.10.0')
  const rejected = await older.release(['1.2.3'])
  assert.equal(rejected.code, 1, rejected.output)
  assert.match(rejected.output, /must be greater than v1.10.0/)
  const huge = fixture(t, { tag: 'v9007199254740993.0.0' })
  huge.git('tag', '--no-sign', '-a', 'v9007199254740992.99.0', '-m', 'Large prior version')
  huge.git('push', 'origin', 'refs/tags/v9007199254740992.99.0')
  const accepted = await huge.release()
  assert.equal(accepted.code, 0, accepted.output)
})

test('dirty or non-main checkouts are preserved and rejected', async t => {
  const dirty = fixture(t)
  writeFileSync(join(dirty.root, 'uncommitted.txt'), 'mock work\n')
  const first = await dirty.release()
  assert.equal(first.code, 1, first.output)
  assert.match(first.output, /Checkout is dirty/)
  assert.equal(readFileSync(join(dirty.root, 'uncommitted.txt'), 'utf8'), 'mock work\n')
  assert.equal(dirty.readState().calls.length, 0)
  const ahead = fixture(t)
  writeFileSync(join(ahead.root, 'unreleased.txt'), 'mock work\n')
  ahead.git('add', 'unreleased.txt')
  ahead.git('commit', '--no-gpg-sign', '-m', 'Unreleased fixture work')
  const head = ahead.git('rev-parse', 'HEAD')
  const second = await ahead.release()
  assert.equal(second.code, 1, second.output)
  assert.match(second.output, /HEAD must equal current origin\/main/)
  assert.equal(ahead.git('rev-parse', 'HEAD'), head)
  assert.equal(ahead.readState().calls.length, 0)
})

test('a lock owned by another invocation is never removed', async t => {
  const f = fixture(t)
  const lock = join(f.root, '.git/sqnsl-release.lock')
  mkdirSync(lock)
  writeFileSync(join(lock, 'owner'), 'another-invocation')
  const result = await f.release()
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /Cannot acquire release lock/)
  assert.equal(readFileSync(join(lock, 'owner'), 'utf8'), 'another-invocation')
})

test('tag pushes do not follow unrelated annotated tags from Git configuration', async t => {
  const f = fixture(t)
  f.git('config', 'push.followTags', 'true')
  f.git('tag', '--no-sign', '-a', 'scratch-tag', '-m', 'Unrelated tag')
  const result = await f.release()
  assert.equal(result.code, 0, result.output)
  assert.equal(f.git('ls-remote', '--refs', 'origin', 'refs/tags/scratch-tag'), '')
})

test('the first packaged release allows an absent GHCR package and formula', async t => {
  const f = fixture(t, { firstRelease: true })
  const result = await f.release()
  assert.equal(result.code, 0, result.output)
  assert.deepEqual(f.journals()[0].baseline.images, [])
  assert.equal(f.journals()[0].baseline.tap.version, null)
})

test('SIGINT cancels and waits for the exact owned run before deleting tags', async t => {
  const f = fixture(t, { scenario: 'running' })
  const result = await f.release(undefined, { signalOn: 'in_progress/pending' })
  assert.equal(result.code, 130, result.output)
  assert.match(result.output, /Rollback complete/)
  assert.equal(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'), '')
  const calls = f.readState().calls
  const cancel = calls.findIndex(call => call.method === 'POST')
  const jobs = calls.findIndex(call => call.endpoint.includes('/attempts/1/jobs'))
  assert.ok(cancel >= 0 && cancel < jobs)
  assert.equal(calls[cancel].endpoint, 'repos/mbianchidev/sql-not-so-lite/actions/runs/123/cancel')
  assert.ok(calls.slice(cancel + 1, jobs).some(call => call.endpoint.endsWith('/actions/runs/123')))
  assert.ok(calls[jobs].remoteTag, 'Tag must still exist while publication is being checked')
  assert.equal(f.readState().cancelled, true)
})

test('a workflow timeout cancels the run and rolls back with a failure exit code', async t => {
  const f = fixture(t, { scenario: 'running' })
  const result = await f.release(undefined, {
    extraEnv: { SQNSL_RELEASE_TIMEOUT_SECONDS: '0.6' },
  })
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /Rollback complete/)
  assert.equal(f.readState().cancelled, true)
  assert.equal(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'), '')
})

test('a cancellation race with a terminal run is rechecked rather than treated as successful cancellation', async t => {
  const f = fixture(t, { scenario: 'cancel-race' })
  const result = await f.release(undefined, { signalOn: 'in_progress/pending' })
  assert.equal(result.code, 130, result.output)
  assert.match(result.output, /Cancellation request failed/)
  assert.match(result.output, /became terminal before cancellation/)
  assert.match(result.output, /Rollback complete/)
})

for (const [name, options, message] of [
  ['cancellation fails', { scenario: 'cancel-failure' }, /Cancellation request failed/],
  ['cancellation never reaches terminal state', { scenario: 'cancel-hangs' }, /deadline|timed out/],
  ['publication job data is inaccessible', { scenario: 'ci-failure', jobsFailure: true }, /HTTP 403/],
  ['publication jobs cannot both be identified', { scenario: 'ci-failure', missingJobs: true }, /Cannot prove both/],
  ['the run was rerun by another actor', { scenario: 'ci-failure', rerunOnRead: true }, /identity\/attempt changed/],
  ['multiple runs claim the tag', { scenario: 'ci-failure', concurrentRun: true }, /Multiple Release runs/],
  ['the run points to a different commit', { scenario: 'ci-failure', wrongHead: true }, /identity\/attempt changed/],
  ['remote tag ownership changes', { scenario: 'ci-failure', changeRemote: true }, /Remote v1.2.3 ownership changed/],
]) {
  test(`rollback is incomplete and preserves tags when ${name}`, async t => {
    const f = fixture(t, options)
    const result = await f.release(undefined, {
      signalOn: options.scenario.startsWith('cancel-') ? 'in_progress/pending' : undefined,
      extraEnv: { SQNSL_RELEASE_ROLLBACK_TIMEOUT_SECONDS: '2' },
    })
    assert.equal(result.code, 2, result.output)
    assert.match(result.output, message)
    assert.match(result.output, /Rollback incomplete/)
    assert.doesNotMatch(result.output, /Rollback complete:/)
    assert.ok(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'))
    assert.equal(f.journals()[0].outcome, 'rollback-incomplete')
    assert.ok(!f.readState().calls.some(call => call.method === 'DELETE'))
    if (options.changeRemote) {
      assert.equal(f.git('--git-dir', f.remote, 'rev-parse', 'refs/tags/v1.2.3'), f.commit)
    }
  })
}

for (const scenario of ['publish-failure', 'homebrew-failure']) {
  test(`${scenario} retains partially published GitHub, GHCR, and Homebrew state for coordinated recovery`, async t => {
    const f = fixture(t, { scenario })
    const result = await f.release()
    assert.equal(result.code, 2, result.output)
    assert.match(result.output, /Publication started or may have started/)
    assert.match(result.output, /GHCR version\/latest.*Homebrew/)
    assert.doesNotMatch(result.output, /Rollback complete:/)
    assert.ok(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'))
    const journal = f.journals()[0]
    assert.equal(journal.baseline.release, null)
    assert.equal(journal.after.release.tag_name, 'v1.2.3')
    assert.ok(journal.after.images.some(image => image.metadata.container.tags.includes('latest')))
    assert.ok(!f.readState().calls.some(call => call.method !== 'GET'))
  })
}

test('a local tag changed by another writer is preserved even when remote cleanup succeeds', async t => {
  const f = fixture(t, { scenario: 'ci-failure', changeLocal: true })
  const result = await f.release()
  assert.equal(result.code, 2, result.output)
  assert.match(result.output, /Local v1.2.3 ownership changed/)
  assert.equal(f.git('rev-parse', 'refs/tags/v1.2.3'), f.commit)
  assert.equal(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'), '')
  assert.equal(f.journals()[0].remoteTagRemoved, true)
})

test('remote deletion denial is reported and the recovery tag is retained', async t => {
  const f = fixture(t, { scenario: 'ci-failure' })
  const hook = join(f.remote, 'hooks/pre-receive')
  writeFileSync(hook, '#!/bin/sh\nwhile read -r old new ref; do\n' +
    '  if [ "$new" = "0000000000000000000000000000000000000000" ]; then\n' +
    '    echo "mock: deletion denied" >&2\n    exit 1\n  fi\ndone\n')
  chmodSync(hook, 0o755)
  const result = await f.release()
  assert.equal(result.code, 2, result.output)
  assert.match(result.output, /deletion denied/)
  assert.match(result.output, /Rollback incomplete/)
  assert.equal(f.git('rev-parse', 'refs/tags/v1.2.3'), f.journals()[0].tagObject)
  assert.ok(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'))
})

test('resources created after initial preflight are checked again before pushing the tag', async t => {
  const f = fixture(t, { imageAfterPreflight: true })
  const result = await f.release()
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /already has.*GHCR/)
  assert.equal(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'), '')
  assert.equal(f.readState().published, undefined)
})

test('a concurrent draft release is detected even when the by-tag endpoint hides it', async t => {
  const f = fixture(t, { draftAfterPreflight: true })
  const result = await f.release(undefined, { extraEnv: { SQNSL_RELEASE_TIMEOUT_SECONDS: '0.5' } })
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /already exists.*GitHub releases/)
  assert.equal(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'), '')
  assert.equal(f.readState().published, undefined)
})

test('a tag replaced before run ownership is established cannot authorize cancellation', async t => {
  const f = fixture(t, { scenario: 'running', changeRemoteBeforeRegistration: true })
  const result = await f.release(undefined, { signalOn: 'in_progress/pending' })
  assert.equal(result.code, 2, result.output)
  assert.ok(!f.readState().calls.some(call => call.method !== 'GET'), result.output)
  assert.equal(f.git('--git-dir', f.remote, 'rev-parse', 'refs/tags/v1.2.3'), f.commit)
  assert.match(result.output, /ownership/)
})

test('the remote deletion lease protects a tag replaced between inspection and push', async t => {
  const f = fixture(t, { scenario: 'ci-failure' })
  const hook = join(f.root, '.git/hooks/pre-push')
  writeFileSync(hook, `#!/usr/bin/env node
const { readFileSync } = require('node:fs')
const { execFileSync } = require('node:child_process')
for (const row of readFileSync(0, 'utf8').trim().split('\\n')) {
  const [, localObject, remoteRef] = row.split(' ')
  if (/^0+$/.test(localObject)) {
    execFileSync('git', ['--git-dir', ${JSON.stringify(f.remote)}, 'update-ref', remoteRef, ${JSON.stringify(f.commit)}])
  }
}
`)
  chmodSync(hook, 0o755)
  const result = await f.release()
  assert.equal(result.code, 2, result.output)
  assert.match(result.output, /Rollback incomplete/)
  assert.equal(f.git('--git-dir', f.remote, 'rev-parse', 'refs/tags/v1.2.3'), f.commit)
  assert.equal(f.git('rev-parse', 'refs/tags/v1.2.3'), f.journals()[0].tagObject)
})

test('unknown workflow registration is bounded and never authorizes tag deletion', async t => {
  const f = fixture(t, { neverRegisters: true })
  const result = await f.release(undefined, {
    extraEnv: { SQNSL_RELEASE_TIMEOUT_SECONDS: '0.5', SQNSL_RELEASE_ROLLBACK_TIMEOUT_SECONDS: '0.5' },
  })
  assert.equal(result.code, 2, result.output)
  assert.match(result.output, /Rollback incomplete/)
  assert.ok(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'))
  assert.ok(!f.readState().calls.some(call => call.method !== 'GET'))
})

test('a hung GitHub command times out before creating release resources', async t => {
  const f = fixture(t, { hangAPI: true })
  const result = await f.release(undefined, {
    extraEnv: { SQNSL_RELEASE_COMMAND_TIMEOUT_SECONDS: '0.5' },
  })
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /timed out/)
  assert.equal(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'), '')
  assert.ok(!existsSync(join(f.root, '.git/sqnsl-release.lock')))
})

for (const [signal, code] of [['SIGTERM', 143], ['SIGHUP', 129]]) {
  test(`${signal} performs the same coordinated rollback as SIGINT`, async t => {
    const f = fixture(t, { scenario: 'running' })
    const result = await f.release(undefined, { signalOn: 'in_progress/pending', signal })
    assert.equal(result.code, code, result.output)
    assert.match(result.output, /Rollback complete/)
    assert.equal(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'), '')
  })
}

test('delayed workflow registration still selects only the new tag run', async t => {
  const f = fixture(t, { registrationDelay: 2 })
  const result = await f.release()
  assert.equal(result.code, 0, result.output)
  assert.match(result.output, /Waiting for the Release workflow/)
  assert.equal(f.journals()[0].runId, 123)
})

for (const [name, options] of [
  ['missing required release assets', { missingAssets: true }],
  ['only a partial architecture image, not the canonical version image', { partialImageOnly: true }],
]) {
  test(`a successful workflow with ${name} is not reported as a verified release`, async t => {
    const f = fixture(t, options)
    const result = await f.release(undefined, {
      extraEnv: { SQNSL_RELEASE_TIMEOUT_SECONDS: '1' },
    })
    assert.equal(result.code, 2, result.output)
    assert.doesNotMatch(result.output, /v1.2.3 released:/)
    assert.match(result.output, /Rollback incomplete/)
    assert.ok(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'))
    assert.ok(!f.readState().calls.some(call => call.method !== 'GET'))
  })
}

test('a rejected tag push is conservatively retained when workflow absence cannot be proved', async t => {
  const f = fixture(t)
  const hook = join(f.remote, 'hooks/pre-receive')
  writeFileSync(hook, '#!/bin/sh\necho "mock: tag creation denied" >&2\nexit 1\n')
  chmodSync(hook, 0o755)
  const result = await f.release(undefined, {
    extraEnv: { SQNSL_RELEASE_ROLLBACK_TIMEOUT_SECONDS: '0.5' },
  })
  assert.equal(result.code, 2, result.output)
  assert.match(result.output, /tag creation denied/)
  assert.match(result.output, /Rollback incomplete/)
  assert.equal(f.git('ls-remote', '--refs', 'origin', 'refs/tags/v1.2.3'), '')
  assert.equal(f.git('rev-parse', 'refs/tags/v1.2.3'), f.journals()[0].tagObject)
})

test('a push URL for another repository is rejected before remote access', async t => {
  const f = fixture(t)
  f.git('config', 'remote.origin.pushurl', 'git@github.com:example/another-repository.git')
  const result = await f.release()
  assert.equal(result.code, 1, result.output)
  assert.match(result.output, /different push destination/)
  assert.equal(f.readState().calls.length, 0)
})
