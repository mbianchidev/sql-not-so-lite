import { spawnSync } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

export function docker(args) {
  const result = spawnSync('docker', args, { encoding: 'utf8', timeout: 180_000 })
  if (result.error) throw new Error(`docker ${args[0]} failed`, { cause: result.error })
  if (result.status !== 0) {
    throw new Error(`docker ${args[0]} failed (${result.status}):\n${result.stdout}${result.stderr}`)
  }
  return result
}

export function runContainer(args) {
  const name = `sqnsl-check-${randomUUID()}`
  let created = false
  try {
    docker(['create', '--name', name, ...args])
    created = true
    docker(['start', name])
    const status = docker(['wait', name]).stdout.trim()
    const logs = docker(['logs', name])
    const output = `${logs.stdout}${logs.stderr}`
    if (status !== '0') throw new Error(`Container exited with ${status}:\n${output}`)
    return output
  } finally {
    if (created) docker(['rm', '--force', name])
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  if (process.argv.length < 3) throw new Error('Usage: node scripts/container.mjs [docker create options] IMAGE COMMAND')
  process.stdout.write(runContainer(process.argv.slice(2)))
}
