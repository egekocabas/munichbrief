#!/usr/bin/env node
import { execFileSync, spawnSync } from 'node:child_process';

const git = (...args) => execFileSync('git', args, { encoding: 'utf8' });
const paths = new Set(git('ls-files', '--cached', '-z').split('\0').filter(Boolean));
const base = process.env.REPOSITORY_SAFETY_BASE;
if (base) {
  if (!/^[a-f0-9]{40}$/.test(base)) {
    throw new Error('REPOSITORY_SAFETY_BASE must be a full Git commit SHA');
  }
  // Include files added and then removed in this PR/push, not just its final tree.
  const range = /^0+$/.test(base) ? 'HEAD' : `${base}..HEAD`;
  for (const path of git('log', '--format=', '--name-only', '--diff-filter=ACMR', '-z', range, '--').split('\0').filter(Boolean)) {
    paths.add(path);
  }
}

const result = spawnSync('git', ['check-ignore', '--no-index', '--stdin', '-z'], {
  input: [...paths].join('\0') + '\0',
  encoding: 'utf8',
});
if (result.error) throw result.error;
if (result.status !== 0 && result.status !== 1) {
  throw new Error(result.stderr || 'git check-ignore failed');
}
const prohibited = result.stdout.split('\0').filter(Boolean).sort();
if (prohibited.length) {
  console.error('Prohibited files are tracked or appear in incoming commits:');
  for (const path of prohibited) console.error(`- ${JSON.stringify(path)}`);
  console.error('Remove these files from the PR history. Rotate any exposed credentials.');
  process.exit(1);
}
console.log('No prohibited files in the index or supplied commit range.');
