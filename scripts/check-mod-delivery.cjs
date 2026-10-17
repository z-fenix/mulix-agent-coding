const fs = require('node:fs')
const path = require('node:path')
const crypto = require('node:crypto')

const OWNED_FILES = Object.freeze([
  '.claude-plugin/plugin.json', 'hooks/hooks.json', 'hooks/register.tsx',
  'hooks/model.ts', 'hooks/workflow.ts', 'hooks/view.tsx', 'types/index.d.ts',
  'tests/model.test.ts', 'tests/workflow.test.ts', 'tests/host-fixture.ts',
  'tests/register.test.tsx', 'README.md',
])

const children = new Map()
for (const file of OWNED_FILES) {
  const parts = file.split('/')
  for (let n = 0; n < parts.length; n++) {
    const parent = parts.slice(0, n).join('/')
    if (!children.has(parent)) children.set(parent, new Set())
    children.get(parent).add(parts[n])
  }
}

const safe = value => value.replace(/[\x00-\x1f\x7f-\x9f\u{61c}\u{200e}-\u{200f}\u{2028}-\u{202e}\u{2066}-\u{2069}]/gu,
  c => `\\u{${c.codePointAt(0).toString(16)}}`)

function inspect(root, label, problems) {
  const files = new Set()
  if (typeof root !== 'string' || root.length === 0) {
    problems.push(`${label}: invalid root`)
    return files
  }
  try {
    const stat = fs.lstatSync(root)
    if (stat.isSymbolicLink() || !stat.isDirectory()) throw new Error()
  } catch {
    problems.push(`${label}: root missing or not an ordinary directory`)
    return files
  }
  function visit(relative) {
    let entries
    try { entries = fs.readdirSync(path.join(root, relative), { withFileTypes: true }) } catch {
      problems.push(`${label}: unreadable directory ${safe(relative || '.')}`)
      return
    }
    const allowed = children.get(relative)
    for (const entry of entries) {
      const rel = relative ? `${relative}/${entry.name}` : entry.name
      // Host-generated declarations/config are never opened or delivered.
      if (rel === '.claude-plugin/types' || rel === 'tsconfig.json') continue
      if (!allowed?.has(entry.name)) {
        problems.push(`${label}: unexpected entry ${safe(rel)}`)
        continue
      }
      if (entry.isSymbolicLink()) {
        problems.push(`${label}: linked entry ${safe(rel)}`)
        continue
      }
      if (children.has(rel)) {
        if (entry.isDirectory()) visit(rel)
        else problems.push(`${label}: non-directory parent ${safe(rel)}`)
      } else if (entry.isFile()) files.add(rel)
      else problems.push(`${label}: non-file entry ${safe(rel)}`)
    }
  }
  visit('')
  for (const file of OWNED_FILES) {
    if (!files.has(file)) problems.push(`${label}: missing ordinary file ${file}`)
  }
  return files
}

function digest(file) {
  return crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')
}

function checkDelivery(repoDir, liveDir) {
  const problems = []
  const repo = inspect(repoDir, 'repo', problems)
  const live = inspect(liveDir, 'live', problems)
  for (const file of OWNED_FILES) {
    if (!repo.has(file) || !live.has(file)) continue
    try {
      if (digest(path.join(repoDir, file)) !== digest(path.join(liveDir, file))) problems.push(`mismatch: ${file}`)
    } catch {
      problems.push(`unreadable owned file: ${file}`)
    }
  }
  return { ok: problems.length === 0, problems }
}

module.exports = { checkDelivery, OWNED_FILES }

if (require.main === module) {
  if (process.argv.length !== 4) {
    process.stderr.write('usage: node scripts/check-mod-delivery.cjs REPO_DIR LIVE_DIR\n')
    process.exitCode = 2
  } else {
    const result = checkDelivery(process.argv[2], process.argv[3])
    process.stdout.write(JSON.stringify(result, null, 2) + '\n')
    process.exitCode = result.ok ? 0 : 1
  }
}
