import { test } from 'node:test'
import assert from 'node:assert/strict'
import { assetName, cachePath, parseChecksums } from './index.js'

test('assetName covers every released target', () => {
  assert.equal(assetName('1.2.3', 'darwin', 'arm64'), 'codebase-analyser-mcp_1.2.3_darwin_arm64')
  assert.equal(assetName('1.2.3', 'darwin', 'x64'), 'codebase-analyser-mcp_1.2.3_darwin_amd64')
  assert.equal(assetName('1.2.3', 'linux', 'arm64'), 'codebase-analyser-mcp_1.2.3_linux_arm64')
  assert.equal(assetName('1.2.3', 'linux', 'x64'), 'codebase-analyser-mcp_1.2.3_linux_amd64')
  assert.equal(assetName('1.2.3', 'win32', 'x64'), 'codebase-analyser-mcp_1.2.3_windows_amd64.exe')
})

test('assetName rejects an unreleased platform with an actionable message', () => {
  assert.throws(() => assetName('1.2.3', 'linux', 'ia32'), /unsupported/i)
  assert.throws(() => assetName('1.2.3', 'win32', 'arm64'), /unsupported/i)
})

test('cachePath is versioned so an upgrade does not reuse the old binary', () => {
  const a = cachePath('1.2.3', 'codebase-analyser-mcp_1.2.3_linux_amd64')
  const b = cachePath('1.2.4', 'codebase-analyser-mcp_1.2.4_linux_amd64')
  assert.notEqual(a, b)
  assert.match(a, /codebase-analyser/)
})

test('parseChecksums reads the sha256sum format', () => {
  const text = [
    'aaaa  codebase-analyser-mcp_1.2.3_linux_amd64',
    'bbbb  codebase-analyser-mcp_1.2.3_darwin_arm64',
    '',
  ].join('\n')
  const sums = parseChecksums(text)
  assert.equal(sums['codebase-analyser-mcp_1.2.3_linux_amd64'], 'aaaa')
  assert.equal(sums['codebase-analyser-mcp_1.2.3_darwin_arm64'], 'bbbb')
})
