import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

export function parseChecksums(text) {
  const checksums = new Map()
  for (const line of text.trim().split(/\r?\n/)) {
    const match = /^([a-f0-9]{64}) [ *](\S+)$/.exec(line)
    if (!match) throw new Error(`Invalid checksum line: ${line}`)
    const [, hash, filename] = match
    if (checksums.has(filename)) throw new Error(`Duplicate checksum: ${filename}`)
    checksums.set(filename, hash)
  }
  return checksums
}

export function generateFormula(tag, text) {
  const version = tag.replace(/^v/, '')
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[a-z0-9]+(?:[.-][a-z0-9]+)*)?$/i.test(version)) {
    throw new Error(`Invalid release version: ${tag}`)
  }
  const checksums = parseChecksums(text)
  function download(os, arch) {
    const filename = `sql-not-so-lite_${version}_${os}_${arch}.tar.gz`
    const hash = checksums.get(filename)
    if (!hash) throw new Error(`Missing checksum: ${filename}`)
    return `      url "https://github.com/mbianchidev/sql-not-so-lite/releases/download/v${version}/${filename}"
      sha256 "${hash}"`
  }

  return `class SqlNotSoLite < Formula
  desc "Lightweight SQLite-as-a-service daemon with an embedded web GUI"
  homepage "https://github.com/mbianchidev/sql-not-so-lite"
  version "${version}"
  license "MIT"

  on_macos do
    on_arm do
${download('darwin', 'arm64')}
    end
    on_intel do
${download('darwin', 'amd64')}
    end
  end

  on_linux do
    on_arm do
${download('linux', 'arm64')}
    end
    on_intel do
${download('linux', 'amd64')}
    end
  end

  def install
    bin.install "sqnsl"
  end

  service do
    run [opt_bin/"sqnsl", "start"]
    keep_alive true
    working_dir Dir.home
    log_path var/"log/sqnsl.log"
    error_log_path var/"log/sqnsl.log"
  end

  test do
    assert_match "sql-not-so-lite #{version}", shell_output("#{bin}/sqnsl version")
  end
end
`
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const [tag, checksumsPath, outputPath] = process.argv.slice(2)
  if (!tag || !checksumsPath || !outputPath || process.argv.length !== 5) {
    throw new Error('Usage: node scripts/homebrew.mjs VERSION CHECKSUMS OUTPUT')
  }
  const formula = generateFormula(tag, readFileSync(checksumsPath, 'utf8'))
  mkdirSync(dirname(outputPath), { recursive: true })
  writeFileSync(outputPath, formula)
  console.log(`Generated Homebrew formula for ${tag}: ${outputPath}`)
}
