// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only
//
// npm-licenses.mjs — collects the licence texts of the npm packages bundled into the admin UI,
// and fails when one is outside the allowed licences (those that would not rule out offering the
// gateway under a commercial licence). Runs in the image's frontend stage, on the tree `npm install`
// just produced, so the texts match what the bundle was built from.
//
//   node scripts/npm-licenses.mjs <web directory> <output directory>
//
// Writes <output>/<name>@<version>/<LICENSE|NOTICE|COPYING files> and <output>/MANIFEST.txt.
// Exit 0 = every production package allowed, 1 = not.
import { execFileSync } from 'node:child_process';
import { copyFileSync, mkdirSync, readdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const [web, out] = process.argv.slice(2);
if (!web || !out) {
  console.error('usage: node npm-licenses.mjs <web directory> <output directory>');
  process.exit(2);
}

const ALLOWED = new Set(['MIT', 'ISC', 'BSD-2-Clause', 'BSD-3-Clause', '0BSD', 'Apache-2.0', 'MPL-2.0']);
// The vendored ui-kit is 3eAI Labs code shipped under the gateway's own licence.
const OWN = new Set(['ui-kit', '@3eai-labs/ui-kit']);

// An SPDX expression is allowed when every AND term, and at least one OR alternative, is.
function allowed(expr) {
  const e = expr.replace(/[()]/g, ' ').trim();
  if (e.includes(' OR ')) return e.split(' OR ').some((x) => allowed(x));
  return e.split(' AND ').every((x) => ALLOWED.has(x.trim()));
}

const pkgs = JSON.parse(execFileSync('npm', ['query', '.prod'], { cwd: web, maxBuffer: 64 << 20 }));
const seen = new Map();
const bad = [];
for (const p of pkgs) {
  if (!p.location) continue; // the root package
  // Our own, by name or, for a link whose target is missing, by where it is installed.
  if (OWN.has(p.name ?? p.location.split('node_modules/').pop())) continue;
  const key = `${p.name}@${p.version}`;
  if (seen.has(key)) continue;
  const lic = typeof p.license === 'string' ? p.license : p.license?.type ?? '';
  const dir = join(web, p.location);
  const files = readdirSync(dir).filter((f) => /^(LICEN[CS]E|NOTICE|COPYING)/i.test(f));
  for (const f of files) {
    mkdirSync(join(out, key), { recursive: true });
    copyFileSync(join(dir, f), join(out, key, f));
  }
  seen.set(key, `${key}\t${lic || '(none)'}\t${files.length ? files.join(',') : '(no licence file in package)'}`);
  if (!lic || !allowed(lic)) bad.push(`${key}: ${lic || 'no licence declared'}`);
}

mkdirSync(out, { recursive: true });
writeFileSync(join(out, 'MANIFEST.txt'), [...seen.values()].sort().join('\n') + '\n');
console.log(`npm-licenses: ${seen.size} production packages`);
if (bad.length) {
  console.error(`npm-licenses: outside ${[...ALLOWED].join(',')}:\n  ${bad.join('\n  ')}`);
  process.exit(1);
}
