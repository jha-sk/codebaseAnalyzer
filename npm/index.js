#!/usr/bin/env node
// Thin launcher for the codebase-analyser MCP server.
//
// On first run it downloads the prebuilt Go binary for this platform from
// GitHub Releases, verifies it against the release checksums, caches it, and
// execs it. Every later run is a cache hit and an exec. Same pattern esbuild
// and biome use to ship a compiled binary through npm.
//
// stdio is inherited, not piped: the parent process IS the MCP host, and the
// JSON-RPC stream must pass through untouched.

import { createHash } from 'node:crypto'
import { spawn } from 'node:child_process'
import { chmodSync, existsSync, mkdirSync, renameSync, writeFileSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { createRequire } from 'node:module'

const REPO = process.env.CODEBASE_ANALYSER_REPO ?? 'jha-sk/codebaseAnalyzer'
const VERSION = createRequire(import.meta.url)('./package.json').version

const ARCH = { arm64: 'arm64', x64: 'amd64' }
const OS = { darwin: 'darwin', linux: 'linux', win32: 'windows' }

// Windows is amd64-only in the release matrix; anything else is a build we
// do not publish, and saying so beats a 404.
export function assetName(version, platform, arch) {
  const goos = OS[platform]
  const goarch = ARCH[arch]
  if (!goos || !goarch || (goos === 'windows' && goarch !== 'amd64')) {
    throw new Error(
      `unsupported platform ${platform}/${arch}. Prebuilt binaries exist for ` +
        `darwin/arm64, darwin/amd64, linux/arm64, linux/amd64, windows/amd64. ` +
        `Build from source: go build ./cmd/codebase-analyser-mcp`,
    )
  }
  return `codebase-analyser-mcp_${version}_${goos}_${goarch}${goos === 'windows' ? '.exe' : ''}`
}

export function cachePath(version, name) {
  const base =
    process.env.XDG_CACHE_HOME ??
    (process.platform === 'darwin'
      ? join(homedir(), 'Library', 'Caches')
      : process.platform === 'win32'
        ? process.env.LOCALAPPDATA ?? join(homedir(), 'AppData', 'Local')
        : join(homedir(), '.cache'))
  return join(base, 'codebase-analyser', 'bin', version, name)
}

export function parseChecksums(text) {
  const out = {}
  for (const line of text.split('\n')) {
    const m = line.trim().match(/^([0-9a-f]+)\s+\*?(.+)$/i)
    if (m) out[m[2]] = m[1].toLowerCase()
  }
  return out
}

async function fetchOrThrow(url) {
  const res = await fetch(url, { redirect: 'follow' })
  if (!res.ok) throw new Error(`GET ${url}: ${res.status} ${res.statusText}`)
  return res
}

async function ensureBinary() {
  const name = assetName(VERSION, process.platform, process.arch)
  const dest = cachePath(VERSION, name)
  if (existsSync(dest)) return dest

  const base = `https://github.com/${REPO}/releases/download/v${VERSION}`
  process.stderr.write(`codebase-analyser-mcp: downloading ${name} (first run only)...\n`)

  const sums = parseChecksums(await (await fetchOrThrow(`${base}/checksums.txt`)).text())
  const want = sums[name]
  if (!want) throw new Error(`${name} is not listed in the release checksums`)

  const body = Buffer.from(await (await fetchOrThrow(`${base}/${name}`)).arrayBuffer())
  const got = createHash('sha256').update(body).digest('hex')
  if (got !== want) throw new Error(`checksum mismatch for ${name}: got ${got}, want ${want}`)

  // Write to a temp file and rename, so an interrupted download never leaves
  // a truncated binary that the next run treats as a cache hit.
  mkdirSync(dirname(dest), { recursive: true })
  const tmp = join(tmpdir(), `${name}.${process.pid}`)
  writeFileSync(tmp, body)
  chmodSync(tmp, 0o755)
  renameSync(tmp, dest)
  return dest
}

async function main() {
  let bin
  try {
    bin = await ensureBinary()
  } catch (err) {
    process.stderr.write(`codebase-analyser-mcp: ${err.message}\n`)
    process.exit(1)
  }
  const child = spawn(bin, process.argv.slice(2), { stdio: 'inherit' })
  child.on('exit', (code, signal) => process.exit(signal ? 1 : (code ?? 0)))
  child.on('error', (err) => {
    process.stderr.write(`codebase-analyser-mcp: ${err.message}\n`)
    process.exit(1)
  })
}

// Only run when invoked as the binary, so the tests can import the helpers.
if (process.argv[1] && process.argv[1].endsWith('index.js')) await main()
