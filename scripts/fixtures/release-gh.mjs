#!/usr/bin/env node
import { execFileSync } from 'node:child_process'
import { readFileSync, renameSync, writeFileSync } from 'node:fs'

const path = process.env.SQNSL_RELEASE_MOCK_STATE
const state = JSON.parse(readFileSync(path, 'utf8'))
const oneOffToken = process.env.GH_TOKEN === 'mock-one-off-release-token'
const args = process.argv.slice(2)
if (args.shift() !== 'api') throw new Error('Only mocked gh api calls are allowed')
let method = 'GET'
let endpoint
let paginate = false
while (args.length) {
  const arg = args.shift()
  if (arg === '--hostname') {
    if (args.shift() !== 'github.com') throw new Error('Unexpected GitHub host')
  } else if (arg === '--method') {
    method = args.shift()
  } else if (arg === '--paginate' || arg === '--slurp') {
    paginate = true
  } else if (arg.startsWith('-') || endpoint) {
    throw new Error(`Unexpected gh argument: ${arg}`)
  } else {
    endpoint = arg
  }
}

const repo = 'repos/mbianchidev/sql-not-so-lite'
const packages = 'users/mbianchidev/packages'
const tag = state.tag
const ref = `refs/tags/${tag}`
const runId = state.runId ?? 123
const git = (...args) => execFileSync('git', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim()
function remoteTag() {
  try {
    return git('--git-dir', state.remote, 'rev-parse', '--verify', ref)
  } catch {
    return null
  }
}
state.calls.push({ method, endpoint, remoteTag: remoteTag(), oneOffToken,
  githubTokenPresent: process.env.GITHUB_TOKEN !== undefined })
function saveState() {
  const temp = `${path}.${process.pid}.tmp`
  writeFileSync(temp, JSON.stringify(state))
  renameSync(temp, path)
}
function finish(body, status = 200) {
  saveState()
  if (status >= 400) {
    console.error(`gh: Mock API failure (HTTP ${status})`)
    if (state.echoRejectedToken && process.env.GH_TOKEN) console.error(`Mock rejected credential: ${process.env.GH_TOKEN}`)
    console.log(JSON.stringify({ message: 'Mock API failure', status: String(status) }))
    process.exit(1)
  }
  if (body !== undefined) console.log(JSON.stringify(paginate ? [body] : body))
}
function release() {
  return {
    id: 51,
    tag_name: tag,
    draft: Boolean(state.draftRelease),
    prerelease: false,
    html_url: `https://github.com/mbianchidev/sql-not-so-lite/releases/tag/${tag}`,
    assets: [
      'checksums.txt',
      ...['darwin', 'linux'].flatMap(os => ['amd64', 'arm64'].flatMap(arch =>
        (os === 'linux' ? ['tar.gz', 'deb', 'rpm'] : ['tar.gz']).map(ext =>
          `sql-not-so-lite_${tag.slice(1)}_${os}_${arch}.${ext}`,
        ),
      )),
    ].filter(name => !state.missingAssets || name !== 'checksums.txt').map(name => ({ name, size: 100 })),
  }
}
function run() {
  const pending = ['running', 'cancel-failure', 'cancel-hangs', 'cancel-race'].includes(state.scenario)
  const completed = !pending || (state.cancelled && state.scenario !== 'cancel-hangs')
  const publishing = ['publish-failure', 'homebrew-failure', 'success'].includes(state.scenario)
  if (completed && publishing) state.published = true
  return {
    id: runId,
    workflow_id: 42,
    head_branch: tag,
    head_sha: state.wrongHead ? 'b'.repeat(40) : state.commit,
    event: 'push',
    path: '.github/workflows/release.yml',
    run_attempt: state.rerun ? 2 : 1,
    status: completed ? 'completed' : 'in_progress',
    conclusion: !completed ? null : state.cancelled ? 'cancelled' :
      state.scenario === 'success' ? 'success' : 'failure',
    html_url: `https://github.com/mbianchidev/sql-not-so-lite/actions/runs/${runId}`,
  }
}

function previousRuns() {
  return (state.previousRuns ?? []).map((run, index) => ({
    id: 100 + index,
    workflow_id: 42,
    head_branch: tag,
    head_sha: run.sameCommit ? state.commit : '9'.repeat(40),
    event: 'push',
    path: '.github/workflows/release.yml',
    run_attempt: 1,
    status: 'completed',
    conclusion: 'failure',
    ...run,
  }))
}

const previousRun = previousRuns().find(run => endpoint === `${repo}/actions/runs/${run.id}` ||
  endpoint?.startsWith(`${repo}/actions/runs/${run.id}/attempts/1/jobs?`))

if (endpoint === repo) {
  if (state.authRequired && !oneOffToken) {
    saveState()
    console.error('gh: Authentication required')
    process.exit(4)
  }
  if (state.authStatus && !oneOffToken) finish(null, state.authStatus)
  if (state.hangAPI) {
    saveState()
    await new Promise(resolve => setTimeout(resolve, 120_000))
  }
  finish({ full_name: 'mbianchidev/sql-not-so-lite', default_branch: 'main',
    archived: false, permissions: { push: !state.readOnly && !(oneOffToken && state.oneOffReadOnly) } })
} else if (endpoint === `${repo}/actions/workflows/release.yml`) {
  finish({ id: 42, path: '.github/workflows/release.yml', state: state.disabled ? 'disabled_manually' : 'active' })
} else if (endpoint?.startsWith(`${repo}/releases?`)) {
  finish([{ tag_name: 'v0.0.1' }, ...(state.existingRelease || state.published || state.foreignRelease ? [release()] : [])])
} else if (endpoint === `${repo}/releases/tags/${tag}`) {
  if ((state.published || state.foreignRelease) && !state.draftRelease) finish(release())
  else finish(null, 404)
} else if (endpoint?.startsWith(`${packages}?`)) {
  if (state.packageStatus) finish(null, state.packageStatus)
  else if (state.noPackageAccess && !oneOffToken) finish(null, 403)
  else finish(state.firstRelease && !state.published ? [] : [{
    name: 'sql-not-so-lite',
    repository: { full_name: 'mbianchidev/sql-not-so-lite' },
  }])
} else if (endpoint?.startsWith(`${packages}/container/sql-not-so-lite/versions?`)) {
  const versions = [{
    id: 10, name: `sha256:${'a'.repeat(64)}`,
    metadata: { container: { tags: state.published ? ['0.0.1'] : ['0.0.1', 'latest'] } },
  }]
  if (state.published || state.existingImage) {
    versions.push({ id: 11, name: `sha256:${'b'.repeat(64)}`,
      metadata: { container: { tags: [state.partialImageOnly ? `${tag.slice(1)}-amd64` : tag.slice(1), 'latest'] } } })
  }
  finish(versions)
} else if (endpoint?.startsWith(`${repo}/actions/workflows/42/runs?`)) {
  state.runLists = (state.runLists ?? 0) + 1
  if (state.previousRerunBeforePush && state.runLists > 1 && !remoteTag()) state.previousRuns[0].run_attempt = 2
  if (state.previousRerunAfterPush && remoteTag()) state.previousRuns[0].run_attempt = 2
  if (state.previousAddedBeforePush && state.runLists > 1 && !remoteTag() && !state.previousAdded) {
    state.previousRuns.push({ id: 110 })
    state.previousAdded = true
  }
  if (!remoteTag() && !state.registered && state.imageAfterPreflight) state.existingImage = true
  if (!remoteTag() && !state.registered && state.draftAfterPreflight) {
    state.foreignRelease = true
    state.draftRelease = true
  }
  if (remoteTag() || state.registered) {
    if (state.changeRemoteBeforeRegistration && !state.changedBeforeRegistration) {
      git('--git-dir', state.remote, 'update-ref', ref, state.commit)
      state.changedBeforeRegistration = true
    }
    state.polls = (state.polls ?? 0) + 1
    if (!state.neverRegisters && state.polls > (state.registrationDelay ?? 0)) state.registered = true
  }
  const runs = [...previousRuns(), ...(state.registered ? [run()] : [])]
  if (state.concurrentRun && state.registered) runs.push({ ...run(), id: runId + 1 })
  runs.push({ id: 999, workflow_id: 42, event: 'push', run_attempt: 1,
    path: '.github/workflows/release.yml', head_branch: 'v99.0.0', head_sha: 'f'.repeat(40),
    status: 'completed', conclusion: 'failure' })
  finish({ workflow_runs: runs })
} else if (previousRun && endpoint === `${repo}/actions/runs/${previousRun.id}`) {
  if (state.previousRerunOnRead) previousRun.run_attempt = 2
  finish(previousRun)
} else if (previousRun) {
  if (previousRun.jobsFailure) finish(null, 503)
  else finish({ jobs: [
    ...(!previousRun.missingJobs ? [
      { id: 4, name: 'Publish release and multiarch image', status: 'completed',
        conclusion: previousRun.publicationStarted ? 'failure' : 'skipped' },
    ] : []),
    ...(!previousRun.withoutHomebrewJob ? [
      { id: 5, name: 'Update Homebrew tap', status: 'completed',
        conclusion: previousRun.homebrewPublished ? 'success' : 'skipped' },
    ] : []),
    ...(previousRun.duplicatePublicationJob ? [
      { id: 6, name: 'Publish release and multiarch image', status: 'completed', conclusion: 'skipped' },
    ] : []),
    ...(previousRun.duplicateHomebrewJob ? [
      { id: 7, name: 'Update Homebrew tap', status: 'completed', conclusion: 'skipped' },
    ] : []),
  ] })
} else if (endpoint === `${repo}/actions/runs/${runId}`) {
  if (state.changeRemote && !state.changedRemote) {
    git('--git-dir', state.remote, 'update-ref', ref, state.commit)
    state.changedRemote = true
  }
  if (state.changeLocal && !state.changedLocal) {
    git('update-ref', ref, state.commit)
    state.changedLocal = true
  }
  if (state.rerunOnRead) state.rerun = true
  finish(run())
} else if (endpoint === `${repo}/actions/runs/${runId}/cancel` && method === 'POST') {
  if (state.scenario === 'cancel-failure') finish(null, 403)
  else {
    state.cancelled = true
    finish(undefined, state.scenario === 'cancel-race' ? 409 : 202)
  }
} else if (endpoint?.startsWith(`${repo}/actions/runs/${runId}/attempts/1/jobs?`)) {
  if (state.jobsFailure) finish(null, 403)
  else {
    const publication = state.published || state.publicationStarted
    finish({ jobs: [
      { id: 1, name: 'checks / CI result', status: 'completed', conclusion: state.cancelled ? 'cancelled' : 'failure' },
      ...(!state.missingJobs ? [
        { id: 2, name: 'Publish release and multiarch image', status: 'completed',
          conclusion: publication ? (['success', 'homebrew-failure'].includes(state.scenario) ? 'success' : 'failure') : 'skipped' },
      ] : []),
      ...(state.scenario === 'homebrew-failure' ? [
        { id: 3, name: 'Update Homebrew tap', status: 'completed', conclusion: 'failure' },
      ] : []),
      ...(state.duplicatePublicationJob ? [
        { id: 4, name: 'Publish release and multiarch image', status: 'completed', conclusion: 'skipped' },
      ] : []),
    ] })
  }
} else {
  throw new Error(`Unmocked API request: ${method} ${endpoint}`)
}
