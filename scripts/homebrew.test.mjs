import assert from 'node:assert/strict'
import { test } from 'node:test'
import { generateFormula } from './homebrew.mjs'

const version = '1.2.3'
const platforms = ['darwin_arm64', 'darwin_amd64', 'linux_arm64', 'linux_amd64']
const checksums = platforms.map((platform, index) =>
  `${String(index + 1).repeat(64)}  sql-not-so-lite_${version}_${platform}.tar.gz`,
).join('\n')

test('generates verified downloads for all four supported platforms', () => {
  const formula = generateFormula(version, checksums)
  for (const [index, platform] of platforms.entries()) {
    assert.ok(formula.includes(
      `https://github.com/mbianchidev/sql-not-so-lite/releases/download/v${version}/sql-not-so-lite_${version}_${platform}.tar.gz`,
    ))
    assert.ok(formula.includes(`sha256 "${String(index + 1).repeat(64)}"`))
  }
  assert.ok(formula.includes('class SqlNotSoLite < Formula'))
  assert.ok(formula.includes('run [opt_bin/"sqnsl", "start"]'))
  assert.ok(formula.includes('bin.install "sqnsl"'))
  assert.ok(formula.includes('shell_output("#{bin}/sqnsl version")'))
})

test('normalizes a version tag and accepts snapshot versions', () => {
  assert.ok(generateFormula(`v${version}`, checksums).includes(`version "${version}"`))
  const snapshot = `${version}-dev.abcdef0`
  assert.ok(generateFormula(
    snapshot,
    checksums.replaceAll(version, snapshot),
  ).includes(`version "${snapshot}"`))
})

test('rejects missing platform archives', () => {
  assert.throws(
    () => generateFormula(version, checksums.split('\n').slice(1).join('\n')),
    /Missing checksum.*darwin_arm64/,
  )
})

test('rejects malformed checksums and duplicate filenames', () => {
  assert.throws(() => generateFormula(version, checksums.replace('1'.repeat(64), 'bad')), /Invalid checksum/)
  assert.throws(() => generateFormula(version, `${checksums}\n${checksums.split('\n')[0]}`), /Duplicate/)
})

test('rejects unsafe or malformed versions', () => {
  for (const invalid of ['', '../1.2.3', '1.2', '01.2.3', '1.2.3"\nrun "unsafe"', '1.2.3-..']) {
    assert.throws(() => generateFormula(invalid, checksums), /Invalid release version/)
  }
})
