#!/usr/bin/env node
// Structural check for docs/ and relative Markdown links across the repository.
// Rules: every docs directory has a README.md index; every page and subdirectory is linked
// from its directory index; every page except docs/README.md carries a breadcrumb line that
// starts with "[Docs]("; every relative Markdown link resolves. No dependencies.
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join, normalize } from 'node:path';
import { fileURLToPath } from 'node:url';

process.chdir(join(dirname(fileURLToPath(import.meta.url)), '..'));

const errors = [];
const walk = (dir) =>
  readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    return statSync(path).isDirectory() ? [path, ...walk(path)] : [path];
  });
const stripCode = (text) => text.replace(/```[\s\S]*?```/g, '').replace(/`[^`\n]*`/g, '');
const linkTargets = (text) => [...stripCode(text).matchAll(/\]\(([^)\s]+)\)/g)].map((m) => m[1]);

const docs = walk('docs');
const docDirs = ['docs', ...docs.filter((p) => statSync(p).isDirectory() && !p.includes('/assets'))];
for (const dir of docDirs) {
  const index = join(dir, 'README.md');
  if (!existsSync(index)) {
    errors.push(`${dir}: missing README.md index`);
    continue;
  }
  const linked = new Set(
    linkTargets(readFileSync(index, 'utf8')).map((t) =>
      normalize(join(dir, t.split('#')[0])).replace(/\/$/, ''),
    ),
  );
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (name === 'README.md' || name === 'assets') continue;
    const isDir = statSync(path).isDirectory();
    if (!isDir && !name.endsWith('.md')) continue;
    if (!linked.has(normalize(path)) && !linked.has(normalize(join(path, 'README.md')))) {
      errors.push(`${index}: does not link ${path}`);
    }
  }
}

for (const page of docs.filter((p) => p.endsWith('.md'))) {
  if (page === join('docs', 'README.md')) continue;
  const head = readFileSync(page, 'utf8').split('\n').slice(0, 6);
  if (!head.some((line) => line.startsWith('[Docs]('))) {
    errors.push(`${page}: missing breadcrumb line under the title`);
  }
}

const rootPages = ['AGENTS.md', 'README.md', 'CONTRIBUTING.md', 'SECURITY.md'].filter(existsSync);
const linkSources = [
  ...docs.filter((p) => p.endsWith('.md')),
  ...walk('openspec').filter((p) => p.endsWith('.md')),
  ...rootPages,
];
for (const file of linkSources) {
  for (const target of linkTargets(readFileSync(file, 'utf8'))) {
    if (/^(https?:|mailto:|#)/.test(target)) continue;
    const path = target.split('#')[0];
    if (path && !existsSync(join(dirname(file), path))) {
      errors.push(`${file}: broken link ${target}`);
    }
  }
}

if (errors.length > 0) {
  for (const error of errors) console.error(error);
  console.error(`docs-check: ${errors.length} problem(s)`);
  process.exit(1);
}
console.log('docs-check: ok');
