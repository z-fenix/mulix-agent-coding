const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')
const { checkDelivery, OWNED_FILES } = require('./check-mod-delivery.cjs')

const expected = [
  '.claude-plugin/plugin.json', 'hooks/hooks.json', 'hooks/register.tsx',
  'hooks/model.ts', 'hooks/workflow.ts', 'hooks/view.tsx', 'types/index.d.ts',
  'tests/model.test.ts', 'tests/workflow.test.ts', 'tests/host-fixture.ts',
  'tests/register.test.tsx', 'README.md',
]

function trees(dir) {
  const repo = path.join(dir, 'repo')
  const live = path.join(dir, 'live')
  for (const root of [repo, live]) {
    for (const f of expected) {
      const p = path.join(root, f)
      fs.mkdirSync(path.dirname(p), { recursive: true })
      fs.writeFileSync(p, 'owned')
    }
  }
  return { repo, live }
}

test('only ordinary owned bytes are delivery inputs', () => {
  assert.deepEqual(OWNED_FILES, expected)
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'mulix-delivery-'))
  try {
    const { repo, live } = trees(dir)
    assert.equal(checkDelivery(repo, live).ok, true)
    fs.writeFileSync(path.join(live, 'hooks/model.ts'), 'PRIVATE_DIFFERENT_BYTES')
    const mismatch = checkDelivery(repo, live)
    assert.equal(mismatch.ok, false)
    assert.equal(JSON.stringify(mismatch).includes('PRIVATE_DIFFERENT_BYTES'), false)
    fs.writeFileSync(path.join(live, 'hooks/model.ts'), 'owned')
    const generated = path.join(live, '.claude-plugin/types/claude-code/index.d.ts')
    fs.mkdirSync(path.dirname(generated), { recursive: true })
    fs.writeFileSync(generated, 'DO_NOT_COPY')
    assert.equal(checkDelivery(repo, live).ok, true)
    const extra = path.join(live, 'hooks/extra.ts')
    fs.writeFileSync(extra, 'DO_NOT_EXECUTE')
    assert.equal(checkDelivery(repo, live).ok, false)
    fs.unlinkSync(extra)
    fs.unlinkSync(path.join(live, 'README.md'))
    assert.equal(checkDelivery(repo, live).ok, false)
    fs.symlinkSync(path.join(repo, 'README.md'), path.join(live, 'README.md'), 'file')
    assert.equal(checkDelivery(repo, live).ok, false)
  } finally {
    fs.rmSync(dir, { recursive: true, force: true })
  }
})

test('linked owned parent is rejected without following it', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'mulix-delivery-parent-'))
  try {
    const { repo, live } = trees(dir)
    const saved = path.join(dir, 'saved-hooks')
    fs.renameSync(path.join(live, 'hooks'), saved)
    fs.symlinkSync(saved, path.join(live, 'hooks'), process.platform === 'win32' ? 'junction' : 'dir')
    assert.equal(checkDelivery(repo, live).ok, false)
  } finally {
    fs.rmSync(dir, { recursive: true, force: true })
  }
})
