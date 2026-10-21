import { execFile } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { existsSync, mkdirSync, readFileSync, realpathSync, renameSync, rmdirSync, unlinkSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { createInterface } from 'node:readline'
import { Writable } from 'node:stream'
import { setTimeout as sleep } from 'node:timers/promises'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { promisify } from 'node:util'

const exec = promisify(execFile)
const repository = 'mbianchidev/sql-not-so-lite'
const tap = 'mbianchidev/homebrew-tap'
const repoAPI = `repos/${repository}`
const stableVersion = /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/
const hash = /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/
const usage = `Usage: ./release.sh X.Y.Z | vX.Y.Z

Run from a clean checkout. Switches to main and pulls origin/main with
fast-forward-only updates before release checks. Requires Node.js 22+, Git,
and gh with repository push/Actions access and read:packages.
Never stashes, resets, or rebases local work. Leaves main checked out.
If authentication is missing or denied, prompts for a hidden, one-off token.
The token is not saved and does not change gh login or environment credentials.
The version bump is a new annotated tag, not a source or frontend version edit.
No main-branch commits are created or pushed.

Exit codes: 0 released, 1 failed with no remaining owned release resources,
2 rollback/recovery incomplete, 64 invalid input, 129 SIGHUP, 130 SIGINT,
143 SIGTERM.
Interrupted clean rollback preserves the signal's exit code.
Recovery journals: the common Git directory's sqnsl-releases/ directory.`

export function normalizeVersion(value) {
  const match = stableVersion.exec(value)
  if (!match || match[0] !== value) {
    throw new Error(`Expected X.Y.Z or vX.Y.Z, without leading zeroes or prerelease suffixes: ${JSON.stringify(value)}`)
  }
  return `v${match.slice(1).join('.')}`
}

function compareVersions(left, right) {
  const a = normalizeVersion(left).slice(1).split('.').map(BigInt)
  const b = normalizeVersion(right).slice(1).split('.').map(BigInt)
  for (let index = 0; index < a.length; index++) {
    if (a[index] !== b[index]) return a[index] > b[index] ? 1 : -1
  }
  return 0
}

function seconds(name, fallback) {
  const value = process.env[name] ?? String(fallback)
  if (!/^\d+(?:\.\d+)?$/.test(value) || !Number.isFinite(Number(value)) ||
    Number(value) <= 0 || Number(value) > 86_400) {
    throw new Error(`${name} must be a positive number of seconds, at most 86400`)
  }
  return Number(value) * 1000
}

class Release {
  constructor(tag, root) {
    this.root = root
    this.tag = tag
    this.ref = `refs/tags/${tag}`
    this.version = tag.slice(1)
    this.abort = new AbortController()
    this.config = {
      timeout: seconds('SQNSL_RELEASE_TIMEOUT_SECONDS', 7200),
      rollback: seconds('SQNSL_RELEASE_ROLLBACK_TIMEOUT_SECONDS', 300),
      poll: seconds('SQNSL_RELEASE_POLL_SECONDS', 10),
      command: seconds('SQNSL_RELEASE_COMMAND_TIMEOUT_SECONDS', 60),
    }
    this.state = { schema: 1, repository, tag, invocation: randomUUID(),
      startedAt: new Date().toISOString(), outcome: 'preflight', pushAttempted: false }
    this.handlers = new Map(['SIGINT', 'SIGTERM', 'SIGHUP'].map(signal => [signal, () => {
      if (!this.interrupted) {
        this.interrupted = signal
        console.error(`Received ${signal}; stopping release work and coordinating rollback.`)
        this.abort.abort(new Error(`Interrupted by ${signal}`))
      }
    }]))
  }

  save() {
    if (!this.journal) return
    writeFileSync(`${this.journal}.tmp`, `${JSON.stringify(this.state, null, 2)}\n`, { mode: 0o600, flag: 'wx' })
    renameSync(`${this.journal}.tmp`, this.journal)
  }

  redact(value) {
    return this.token ? value.replaceAll(this.token, '[REDACTED]') : value
  }

  async promptForToken() {
    if (!process.stdin.isTTY || !process.stderr.isTTY) {
      throw new Error('Cannot prompt for a one-off GitHub token without an interactive terminal. ' +
        'Run ./release.sh in a terminal, or supply GH_TOKEN with repository/Actions access and read:packages.')
    }
    this.abort.signal.throwIfAborted()
    console.error('Provide a token with repository/Actions access and read:packages; it will not be saved.')
    return new Promise((resolve, reject) => {
      const terminal = createInterface({ input: process.stdin, terminal: true, historySize: 0,
        output: new Writable({ write(chunk, encoding, callback) { callback() } }) })
      let settled = false
      const finish = (error, token) => {
        if (settled) return
        settled = true
        clearTimeout(timer)
        this.abort.signal.removeEventListener('abort', onAbort)
        terminal.close()
        process.stderr.write('\n')
        if (error) reject(error)
        else resolve(token)
      }
      const onAbort = () => finish(this.abort.signal.reason)
      const timer = setTimeout(() => finish(new Error('Timed out waiting for the one-off GitHub token.')),
        this.config.command)
      this.abort.signal.addEventListener('abort', onAbort, { once: true })
      terminal.once('SIGINT', this.handlers.get('SIGINT'))
      terminal.once('error', error => finish(new Error('Cannot read the one-off GitHub token.', { cause: error })))
      terminal.once('close', () => finish(new Error('Token entry cancelled before a token was submitted.')))
      terminal.once('line', token => {
        if (!/^[\x21-\x7e]+$/.test(token)) {
          finish(new Error('A nonempty GitHub token without whitespace or control characters is required.'))
        } else {
          finish(null, token)
        }
      })
      process.stderr.write('One-off GitHub token (hidden, this run only): ')
    })
  }

  async command(program, args, { input, allow = [] } = {}) {
    if (!this.cleaning && this.abort.signal.aborted) throw this.abort.signal.reason
    const deadline = this.cleaning ? this.cleanupDeadline : this.deadline
    const remaining = deadline === undefined ? this.config.command : deadline - Date.now()
    if (remaining <= 0) throw new Error(`${this.cleaning ? 'Rollback' : 'Release'} deadline exceeded`)
    const env = { ...process.env, GIT_TERMINAL_PROMPT: '0', GH_PROMPT_DISABLED: '1', GH_PAGER: 'cat', GIT_PAGER: 'cat' }
    if (program === 'gh' && this.token) {
      env.GH_TOKEN = this.token
      delete env.GITHUB_TOKEN
    }
    try {
      const promise = exec(program, args, {
        cwd: this.root,
        env,
        encoding: 'utf8',
        timeout: Math.max(1, Math.min(this.config.command, remaining)),
        killSignal: 'SIGKILL',
        maxBuffer: 16 * 1024 * 1024,
        signal: this.cleaning ? undefined : this.abort.signal,
      })
      let inputError
      if (input !== undefined) {
        promise.child.stdin.on('error', error => { inputError = error })
        promise.child.stdin.end(input)
      }
      const { stdout } = await promise
      if (inputError) throw inputError
      return stdout.trim()
    } catch (cause) {
      if (allow.includes(cause.code)) return (cause.stdout ?? '').trim()
      const detail = this.redact((cause.stderr || cause.stdout || cause.message).trim())
      const error = new Error(`${program} ${args.join(' ')} failed${cause.killed ? ' (timed out)' : ''}:\n${detail}`, { cause })
      error.status = Number(/\(HTTP (\d+)\)/.exec(detail)?.[1] ?? 0)
      throw error
    }
  }

  git(...args) {
    return this.command('git', args)
  }

  async api(path, { pages = false, missing = false, method = 'GET' } = {}) {
    try {
      const output = await this.command('gh', ['api', '--hostname', 'github.com', '--method', method,
        path, ...(pages ? ['--paginate', '--slurp'] : [])])
      if (!output && method === 'GET') throw new Error(`GitHub returned an empty response: ${path}`)
      return output ? JSON.parse(output) : null
    } catch (error) {
      if (missing && error.status === 404) return null
      throw error
    }
  }

  async items(path, key) {
    const pages = await this.api(path, { pages: true })
    if (!Array.isArray(pages)) throw new Error(`Expected paginated GitHub response: ${path}`)
    return pages.flatMap(page => {
      const values = key ? page[key] : page
      if (!Array.isArray(values)) throw new Error(`Expected GitHub list: ${path}`)
      return values
    })
  }

  async cleanCheckout() {
    if (await this.git('status', '--porcelain=v1', '--untracked-files=normal')) {
      throw new Error('Checkout is dirty. Commit or move changes before releasing.')
    }
    if (await this.git('rev-parse', 'HEAD') !== this.state.commit) {
      throw new Error('HEAD changed during this invocation; refusing to release concurrent work.')
    }
  }

  requireNewer(value, source) {
    const match = typeof value === 'string' && stableVersion.exec(value)
    if (!match || match[0] !== value) return
    const comparison = compareVersions(this.tag, value)
    if (comparison <= 0) {
      throw new Error(comparison === 0 ? `${this.tag} already exists in ${source}. Choose a new version.` :
        `${this.tag} must be greater than ${value} in ${source}.`)
    }
  }

  async remoteRefs() {
    const output = await this.git('ls-remote', '--refs', 'origin', 'refs/heads/main', 'refs/tags/*')
    return new Map(output.split('\n').filter(Boolean).map(line => {
      const [object, ref] = line.split(/\s+/)
      if (!hash.test(object) || !ref) throw new Error('Invalid remote ref advertisement')
      return [ref, object]
    }))
  }

  async checkRemote() {
    const refs = await this.remoteRefs()
    if (refs.get('refs/heads/main') !== this.state.commit) {
      throw new Error('HEAD must equal current origin/main after pulling. ' +
        'Local main has unpushed commits or origin/main changed; resolve this before retrying.')
    }
    for (const ref of refs.keys()) {
      if (ref.startsWith('refs/tags/')) this.requireNewer(ref.slice(10), 'remote tags')
    }
  }

  async snapshot() {
    const releases = await this.items(`${repoAPI}/releases?per_page=100`)
    if (releases.some(release => typeof release.tag_name !== 'string')) {
      throw new Error('Invalid GitHub release inventory')
    }
    // Listing with push access includes drafts that the by-tag endpoint can hide.
    const matches = releases.filter(release => [this.tag, this.version].includes(release.tag_name))
    if (matches.length > 1) throw new Error('Conflicting GitHub releases share the normalized version')
    const release = matches[0] ?? null
    let packages
    try {
      packages = await this.items('users/mbianchidev/packages?package_type=container&per_page=100')
    } catch (cause) {
      throw new Error(cause.status === 401 || cause.status === 403 ?
        'Cannot inventory GHCR safely. The gh token needs read:packages.' :
        'Cannot inventory GHCR safely. Check GitHub connectivity and package access.', { cause })
    }
    const pkg = packages.find(pkg => pkg.name === 'sql-not-so-lite')
    if (pkg?.repository?.full_name && pkg.repository.full_name !== repository) {
      throw new Error('GHCR package belongs to another source repository; refusing publication.')
    }
    const images = pkg ? await this.items(
      'users/mbianchidev/packages/container/sql-not-so-lite/versions?per_page=100',
    ) : []
    for (const image of images) {
      if (!Array.isArray(image.metadata?.container?.tags)) throw new Error('Invalid GHCR tag inventory')
    }
    const head = (await this.api(`repos/${tap}/git/ref/heads/main`)).object?.sha
    if (!hash.test(head)) throw new Error('Cannot identify the Homebrew tap main revision')
    const formula = await this.api(
      `repos/${tap}/contents/Formula/sql-not-so-lite.rb?ref=${head}`, { missing: true },
    )
    let formulaVersion = null
    if (formula) {
      if (formula.encoding !== 'base64' || typeof formula.content !== 'string' || !hash.test(formula.sha)) {
        throw new Error('Unexpected Homebrew formula content or identity')
      }
      const text = Buffer.from(formula.content, 'base64').toString('utf8')
      formulaVersion = /^\s*version "([^"]+)"$/m.exec(text)?.[1]
      if (!formulaVersion) throw new Error('Cannot identify the current Homebrew formula version')
      normalizeVersion(formulaVersion)
    }
    return { release, versions: releases.map(release => release.tag_name), images,
      tap: { head, blob: formula?.sha ?? null, version: formulaVersion } }
  }

  hasImage(snapshot, includePartial = true) {
    const targets = includePartial ? [this.version, `${this.version}-amd64`, `${this.version}-arm64`, this.tag] : [this.version]
    return snapshot.images.some(image => image.metadata.container.tags.some(tag => targets.includes(tag)))
  }

  requireUnclaimed(snapshot) {
    for (const version of snapshot.versions) this.requireNewer(version, 'GitHub releases')
    if (snapshot.release || this.hasImage(snapshot)) {
      throw new Error(`${this.tag} already has GitHub/GHCR resources. Choose a new version; nothing was published by this invocation.`)
    }
    for (const image of snapshot.images) {
      for (const tag of image.metadata.container.tags) this.requireNewer(tag, 'GHCR')
    }
    this.requireNewer(snapshot.tap.version, 'Homebrew')
  }

  async runs() {
    const runs = await this.items(`${repoAPI}/actions/workflows/${this.state.workflowId}/runs?` +
      `event=push&branch=${encodeURIComponent(this.tag)}&per_page=100`, 'workflow_runs')
    const matches = runs.filter(run => run.workflow_id === this.state.workflowId &&
      run.head_branch === this.tag && run.event === 'push')
    if (new Set(matches.map(run => run.id)).size !== matches.length) {
      throw new Error('Duplicate Release run identities; refusing ambiguous ownership.')
    }
    const previous = new Map((this.state.previousRuns ?? []).map(run => [run.id, run]))
    const current = matches.filter(run => {
      const recorded = previous.get(run.id)
      if (!recorded) return true
      this.verifyRun(run, { commit: recorded.head_sha, id: recorded.id })
      if (run.status !== 'completed' || run.conclusion !== recorded.conclusion) {
        throw new Error(`Historical Release run ${run.id} changed; refusing uncertain ownership.`)
      }
      previous.delete(run.id)
      return false
    })
    if (previous.size) throw new Error('Historical Release run visibility changed; manual recovery required.')
    return current
  }

  verifyRun(run, { commit = this.state.commit, id = this.state.runId } = {}) {
    if (!Number.isSafeInteger(run.id) || run.id <= 0 ||
      run.workflow_id !== this.state.workflowId || run.head_branch !== this.tag ||
      !hash.test(run.head_sha) || run.head_sha !== commit || run.event !== 'push' ||
      run.path !== '.github/workflows/release.yml' || run.run_attempt !== 1 ||
      (id !== undefined && run.id !== id)) {
      throw new Error('Release workflow identity/attempt changed; refusing to touch concurrent work.')
    }
  }

  async publicationJobs(runId) {
    const jobs = await this.items(`${repoAPI}/actions/runs/${runId}/attempts/1/jobs?per_page=100`, 'jobs')
    const names = ['Publish release and multiarch image', 'Update Homebrew tap']
    const publication = jobs.filter(job => names.includes(job.name))
      .map(({ id, name, status, conclusion }) => ({ id, name, status, conclusion }))
    if (publication.length !== 2 || !names.every(name => publication.filter(job => job.name === name).length === 1)) {
      throw new Error(`Cannot prove both publication jobs were skipped for Release run ${runId}. ` +
        'Tags and release resources preserved.')
    }
    return publication
  }

  async checkPreviousRuns() {
    const runs = await this.runs()
    if (this.state.previousRuns !== undefined) {
      if (runs.length) throw new Error('Release run history changed before tag creation. Nothing was pushed.')
      return
    }
    const previous = []
    for (const run of runs) {
      this.verifyRun(run, { commit: run.head_sha, id: run.id })
      if (run.status !== 'completed' || !['failure', 'cancelled', 'timed_out'].includes(run.conclusion)) {
        throw new Error(`Historical Release run ${run.id} is not a failed, completed first attempt; refusing to reuse ${this.tag}.`)
      }
      const publicationJobs = await this.publicationJobs(run.id)
      if (publicationJobs.some(job => job.status !== 'completed' || job.conclusion !== 'skipped')) {
        throw new Error(`Historical Release run ${run.id} started or may have started publication; refusing to reuse ${this.tag}.`)
      }
      const live = await this.api(`${repoAPI}/actions/runs/${run.id}`)
      this.verifyRun(live, { commit: run.head_sha, id: run.id })
      if (live.status !== run.status || live.conclusion !== run.conclusion) {
        throw new Error(`Historical Release run ${run.id} changed while checking publication; refusing to reuse ${this.tag}.`)
      }
      previous.push({ id: run.id, head_sha: run.head_sha, run_attempt: 1, conclusion: run.conclusion, publicationJobs })
    }
    this.state.previousRuns = previous
    if (previous.length) console.log(`Verified ${previous.length} earlier ${this.tag} run(s) stopped before publication; retrying safely.`)
  }

  async claimRun(run) {
    this.verifyRun(run)
    if (this.state.runId === undefined) {
      if ((await this.remoteRefs()).get(this.ref) !== this.state.tagObject) {
        throw new Error('Cannot establish workflow ownership: the remote tag was removed or replaced. No run will be canceled.')
      }
      this.state.runId = run.id
      this.state.runAttempt = 1
      this.state.runURL = run.html_url
      this.save()
    }
  }

  async trackedRun() {
    const runs = await this.runs()
    if (runs.length !== 1) throw new Error(`Expected one owned Release run, found ${runs.length}; manual recovery required.`)
    this.verifyRun(runs[0])
    const run = await this.api(`${repoAPI}/actions/runs/${runs[0].id}`)
    this.verifyRun(run)
    return run
  }

  async preflight() {
    if (realpathSync(await this.git('rev-parse', '--show-toplevel')) !== realpathSync(this.root)) {
      throw new Error('The release entry point must belong to the repository root')
    }
    this.state.commit = await this.git('rev-parse', '--verify', 'HEAD^{commit}')
    await this.cleanCheckout()
    const urls = (await this.git('config', '--get-all', 'remote.origin.url')).split('\n')
    const allowed = ['git@github.com:mbianchidev/sql-not-so-lite.git',
      'https://github.com/mbianchidev/sql-not-so-lite.git',
      'https://github.com/mbianchidev/sql-not-so-lite',
      'ssh://git@github.com/mbianchidev/sql-not-so-lite.git']
    if (urls.length !== 1 || !allowed.includes(urls[0])) throw new Error(`origin must be ${repository} on github.com`)
    const pushURLs = await this.command('git', ['config', '--get-all', 'remote.origin.pushurl'], { allow: [1] })
    if (pushURLs && !allowed.includes(pushURLs)) throw new Error('origin has a different push destination; refusing release.')
    const gitDir = resolve(this.root, await this.git('rev-parse', '--git-dir'))
    for (const name of ['MERGE_HEAD', 'CHERRY_PICK_HEAD', 'REVERT_HEAD', 'rebase-merge', 'rebase-apply', 'BISECT_LOG']) {
      if (existsSync(join(gitDir, name))) throw new Error(`Finish the active Git operation first: ${name}`)
    }
    const common = resolve(this.root, await this.git('rev-parse', '--git-common-dir'))
    this.lock = join(common, 'sqnsl-release.lock')
    try {
      mkdirSync(this.lock, { mode: 0o700 })
    } catch (cause) {
      throw new Error(`Cannot acquire release lock: ${this.lock}. Check for another release process before removing a stale lock.`, { cause })
    }
    this.ownsLock = true
    writeFileSync(join(this.lock, 'owner'), this.state.invocation, { mode: 0o600, flag: 'wx' })
    const journals = join(common, 'sqnsl-releases')
    mkdirSync(journals, { recursive: true, mode: 0o700 })
    this.journal = join(journals, `${this.tag}-${this.state.invocation}.json`)
    this.save()
    console.log('Switching to main and pulling origin/main (fast-forward only).')
    await this.git('switch', 'main')
    await this.git('pull', '--ff-only', '--no-rebase', '--no-autostash', '--no-tags', 'origin', 'main')
    this.state.commit = await this.git('rev-parse', '--verify', 'HEAD^{commit}')
    await this.cleanCheckout()
    this.save()
    console.log(`Preflight ${this.tag} at ${this.state.commit}. Recovery journal: ${this.journal}`)
    await this.checkRemote()
    for (const tag of (await this.git('tag', '--list')).split('\n')) this.requireNewer(tag, 'local tags')
    this.committer = await this.git('var', 'GIT_COMMITTER_IDENT')
    try {
      await this.githubPreflight()
    } catch (error) {
      if (![401, 403].includes(error.status ?? error.cause?.status) && error.cause?.code !== 4) throw error
      console.error(error.message)
      this.token = await this.promptForToken()
      await this.githubPreflight()
    }
    this.save()
  }

  async githubPreflight() {
    const repo = await this.api(repoAPI)
    if (repo.full_name !== repository || repo.default_branch !== 'main' || repo.archived || !repo.permissions?.push) {
      throw new Error('Need push access to the active source repository with main as its default branch.')
    }
    const workflow = await this.api(`${repoAPI}/actions/workflows/release.yml`)
    if (workflow.state !== 'active' || workflow.path !== '.github/workflows/release.yml' ||
      !Number.isSafeInteger(workflow.id)) throw new Error('Release workflow is missing or disabled.')
    this.state.workflowId = workflow.id
    this.state.baseline = await this.snapshot()
    this.requireUnclaimed(this.state.baseline)
    await this.checkPreviousRuns()
  }

  async pause(deadline) {
    const remaining = deadline - Date.now()
    if (remaining <= 0) throw new Error(`${this.cleaning ? 'Rollback' : 'Release'} deadline exceeded`)
    await sleep(Math.min(this.config.poll, remaining), undefined,
      this.cleaning ? undefined : { signal: this.abort.signal })
  }

  async waitForRun() {
    const registrationDeadline = Math.min(this.deadline, Date.now() + 180_000)
    while (true) {
      const runs = await this.runs()
      if (runs.length > 1) throw new Error('Multiple Release runs match this tag; refusing ambiguous ownership.')
      if (runs.length) {
        await this.claimRun(runs[0])
        return
      }
      console.log(`Waiting for the Release workflow for ${this.tag} to register...`)
      await this.pause(registrationDeadline)
    }
  }

  async publish() {
    await this.cleanCheckout()
    this.state.beforePush = await this.snapshot()
    this.save()
    this.requireUnclaimed(this.state.beforePush)
    await this.cleanCheckout()
    await this.checkRemote()
    await this.checkPreviousRuns()
    this.state.tagObject = await this.command('git', ['mktag'], {
      input: `object ${this.state.commit}\ntype commit\ntag ${this.tag}\n` +
        `tagger ${this.committer}\n\nRelease ${this.tag}\n\nRelease-Invocation: ${this.state.invocation}\n`,
    })
    if (!hash.test(this.state.tagObject)) throw new Error('Git did not create a valid annotated tag object')
    this.state.localCreationAttempted = true
    this.save()
    await this.git('update-ref', this.ref, this.state.tagObject, '0'.repeat(this.state.commit.length))
    this.state.pushAttempted = true
    this.save()
    await this.git('-c', 'remote.origin.mirror=false', '-c', 'push.followTags=false',
      'push', '--porcelain', 'origin', `${this.state.tagObject}:${this.ref}`)
    this.state.pushConfirmed = true
    this.save()
    this.deadline = Date.now() + this.config.timeout
    console.log(`Version bumped to ${this.tag}; only the annotated tag was pushed.`)
    await this.waitForRun()
    let previous
    while (true) {
      const run = await this.trackedRun()
      const status = `${run.status}/${run.conclusion ?? 'pending'}`
      if (status !== previous) console.log(`Release run ${run.id}, attempt 1: ${status}. ${run.html_url}`)
      previous = status
      if (run.status === 'completed') {
        this.state.runConclusion = run.conclusion
        this.save()
        if (run.conclusion !== 'success') throw new Error(`Release run ${run.id} finished with ${run.conclusion}. Inspect ${run.html_url}`)
        break
      }
      await this.pause(this.deadline)
    }
    while (true) {
      this.state.after = await this.snapshot()
      const release = this.state.after.release
      const names = new Set(release?.assets?.filter(asset => asset.size > 0).map(asset => asset.name))
      const expected = ['checksums.txt', ...['darwin', 'linux'].flatMap(os =>
        ['amd64', 'arm64'].flatMap(arch => (os === 'linux' ? ['tar.gz', 'deb', 'rpm'] : ['tar.gz'])
          .map(ext => `sql-not-so-lite_${this.version}_${os}_${arch}.${ext}`)))]
      if (release?.tag_name === this.tag && !release.draft && !release.prerelease &&
        expected.every(name => names.has(name)) && this.hasImage(this.state.after, false) &&
        this.state.after.tap.version && compareVersions(this.state.after.tap.version, this.tag) >= 0) break
      console.log('Waiting for published GitHub assets, GHCR metadata, and the Homebrew formula...')
      await this.pause(this.deadline)
    }
    const finalRun = await this.trackedRun()
    if (finalRun.status !== 'completed' || finalRun.conclusion !== 'success') {
      throw new Error('Release workflow changed during publication verification; manual recovery required.')
    }
    if ((await this.remoteRefs()).get(this.ref) !== this.state.tagObject) {
      throw new Error('Remote release tag changed during publication; manual recovery required.')
    }
    this.state.outcome = 'released'
    this.save()
    console.log(`${this.tag} released: ${this.state.after.release.html_url}`)
  }

  async removeLocalTag() {
    const current = await this.command('git', ['rev-parse', '--verify', '--quiet', this.ref], { allow: [1] })
    if (!current) return
    if (current !== this.state.tagObject) {
      throw new Error(`Local ${this.tag} ownership changed; preserving the other writer's tag.`)
    }
    await this.git('update-ref', '-d', this.ref, this.state.tagObject)
    this.state.localTagRemoved = true
    this.save()
  }

  async rollback() {
    this.cleaning = true
    this.cleanupDeadline = Date.now() + this.config.rollback
    this.state.outcome = 'rolling-back'
    this.save()
    console.log(`Coordinating rollback for ${this.tag}...`)
    if (this.state.pushAttempted) {
      while (true) {
        const runs = await this.runs()
        if (runs.length > 1) throw new Error('Multiple Release runs exist; ownership is ambiguous. Tags preserved.')
        if (runs.length) {
          await this.claimRun(runs[0])
          break
        }
        console.log('Waiting to identify the tag push workflow before any cleanup...')
        await this.pause(this.cleanupDeadline)
      }
      let run = await this.trackedRun()
      if (run.status !== 'completed') {
        console.log(`Canceling owned Release run ${run.id}, attempt 1; waiting for it to stop.`)
        try {
          await this.api(`${repoAPI}/actions/runs/${run.id}/cancel`, { method: 'POST' })
        } catch (error) {
          console.error(`Cancellation request failed: ${error.message}`)
          run = await this.trackedRun()
          if (run.status !== 'completed') throw error
          console.log('The run became terminal before cancellation; checking publication before cleanup.')
        }
        while (run.status !== 'completed') {
          await this.pause(this.cleanupDeadline)
          run = await this.trackedRun()
        }
      }
      this.state.runConclusion = run.conclusion
      this.state.publicationJobs = await this.publicationJobs(run.id)
      this.save()
      this.state.after = await this.snapshot()
      this.save()
      if (this.state.publicationJobs.some(job => job.status !== 'completed' || job.conclusion !== 'skipped')) {
        throw new Error('Publication started or may have started. GitHub assets, GHCR version/latest images, ' +
          'and the Homebrew formula require coordinated recovery, not automatic deletion.')
      }
      if (this.state.after.release || this.hasImage(this.state.after) || this.state.after.tap.version === this.version) {
        throw new Error('Release resources appeared despite skipped publication; ownership is uncertain. Nothing will be deleted.')
      }
      if ((await this.trackedRun()).status !== 'completed') {
        throw new Error('Release run is no longer terminal; tags preserved.')
      }
      const current = (await this.remoteRefs()).get(this.ref)
      if (current && current !== this.state.tagObject) {
        throw new Error(`Remote ${this.tag} ownership changed; preserving the other writer's tag.`)
      }
      if (current) {
        // The lease is a compare-and-delete of this tag, never a branch/history rewrite.
        await this.git('-c', 'remote.origin.mirror=false', '-c', 'push.followTags=false',
          'push', '--porcelain', `--force-with-lease=${this.ref}:${this.state.tagObject}`, 'origin', `:${this.ref}`)
        this.state.remoteTagRemoved = true
        this.save()
        if ((await this.remoteRefs()).has(this.ref)) {
          throw new Error('A remote tag appeared during cleanup; preserving it and requiring manual recovery.')
        }
      }
      if ((await this.trackedRun()).status !== 'completed') {
        throw new Error('A workflow restarted during cleanup; manual recovery required.')
      }
    }
    await this.removeLocalTag()
    this.state.outcome = 'rolled-back'
    this.save()
    console.log(`Rollback complete: owned ${this.tag} tags removed; no published resources were changed.`)
  }

  async execute() {
    for (const [signal, handler] of this.handlers) process.on(signal, handler)
    let code = 0
    try {
      await this.preflight()
      await this.publish()
    } catch (error) {
      console.error(this.redact(error.message))
      if (error.cause?.message && error.cause.message !== error.message) console.error(this.redact(error.cause.message))
      this.state.error = this.redact(error.message)
      code = this.interrupted ? { SIGINT: 130, SIGTERM: 143, SIGHUP: 129 }[this.interrupted] : 1
      if (this.state.localCreationAttempted) {
        try {
          await this.rollback()
        } catch (failure) {
          this.state.outcome = 'rollback-incomplete'
          this.state.rollbackError = failure.message
          console.error(`Rollback incomplete: ${failure.message}`)
          console.error(`Recovery journal: ${this.journal}`)
          if (this.state.runURL) console.error(`Inspect the exact Release run: ${this.state.runURL}`)
          console.error('Do not retag, delete existing releases, reset GHCR latest, or revert the tap blindly. ' +
            'Confirm publication is stopped, compare the journal snapshots, then explicitly recover or complete publication.')
          code = 2
        }
      } else {
        this.state.outcome = 'preflight-failed'
      }
    } finally {
      try {
        this.save()
        if (this.ownsLock) {
          if (readFileSync(join(this.lock, 'owner'), 'utf8') !== this.state.invocation) {
            throw new Error(`Release lock ownership changed; leaving ${this.lock} untouched.`)
          }
          unlinkSync(join(this.lock, 'owner'))
          rmdirSync(this.lock)
        }
      } catch (error) {
        console.error(`Local recovery metadata cleanup failed: ${error.message}`)
        code = 2
      }
      for (const [signal, handler] of this.handlers) process.off(signal, handler)
      this.token = undefined
    }
    return code
  }
}

async function main(args) {
  if (args.length === 1 && args[0] === '--help') {
    console.log(usage)
    return 0
  }
  let tag
  try {
    if (args.length !== 1) throw new Error('Exactly one release version is required.')
    tag = normalizeVersion(args[0])
  } catch (error) {
    console.error(`${error.message}\n\n${usage}`)
    return 64
  }
  try {
    return await new Release(tag, resolve(dirname(fileURLToPath(import.meta.url)), '..')).execute()
  } catch (error) {
    console.error(error.message)
    return 1
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  process.exitCode = await main(process.argv.slice(2))
}
