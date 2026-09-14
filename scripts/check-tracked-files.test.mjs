import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
function repository(t) {
  const cwd = mkdtempSync(join(tmpdir(), 'munichbrief-file-guard-'));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const git = (...args) => execFileSync('git', args, { cwd, encoding: 'utf8' }).trim();
  git('init', '--quiet');
  git('config', 'user.name', 'Fixture');
  git('config', 'user.email', 'fixture@example.invalid');
  git('config', 'commit.gpgsign', 'false');
  copyFileSync(join(root, '.gitignore'), join(cwd, '.gitignore'));
  git('add', '.gitignore');
  git('commit', '--quiet', '-m', 'chore: initialize fixture');
  const add = (path) => {
    mkdirSync(dirname(join(cwd, path)), { recursive: true });
    writeFileSync(join(cwd, path), 'synthetic fixture\n');
    git('add', '--force', '--', path);
  };
  const check = (base = '') => spawnSync(process.execPath, [join(root, 'scripts/check-tracked-files.mjs')], {
    cwd, encoding: 'utf8', env: { ...process.env, REPOSITORY_SAFETY_BASE: base },
  });
  return { git, add, check };
}

test('allows synthetic examples and normal project files', (t) => {
  const { add, check } = repository(t);
  for (const path of ['.env.example', 'nested/.env.sample', 'deploy/example-values.yaml', 'internal/source/fixtures/releases.json', 'LICENSES/sources/example.tar.xz']) add(path);
  const result = check();
  assert.equal(result.status, 0, result.stderr);
});

test('rejects force-added credentials, databases, backups, and private values', (t) => {
  const { add, check } = repository(t);
  const prohibited = ['.env', 'nested/.env.production', 'key.pem', 'tls.key', 'identity.p12', 'identity.pfx', 'snapshot.db', 'snapshot.db-wal', 'snapshot.db.gz', 'nested/cache.sqlite-shm', 'snapshot.sqlite3', 'backups/snapshot.gz', 'server.log', 'deploy/private-values.yaml', 'deploy/private-values.yml'];
  for (const path of prohibited) add(path);
  const result = check();
  assert.equal(result.status, 1, result.stderr);
  for (const path of prohibited) assert.ok(result.stderr.includes(JSON.stringify(path)), path);
  assert.ok(!result.stderr.includes('synthetic fixture'));
});

test('rejects a prohibited file removed before the final PR commit', (t) => {
  const { git, add, check } = repository(t);
  const base = git('rev-parse', 'HEAD');
  add('nested/temporary.db');
  git('commit', '--quiet', '-m', 'chore: add synthetic database');
  git('rm', '--quiet', 'nested/temporary.db');
  git('commit', '--quiet', '-m', 'chore: remove synthetic database');
  assert.equal(check().status, 0);
  const result = check(base);
  assert.equal(result.status, 1, result.stderr);
  assert.ok(result.stderr.includes('nested/temporary.db'));
});

test('rejects an invalid or unavailable base instead of skipping history', (t) => {
  const { check } = repository(t);
  assert.notEqual(check('--all').status, 0);
  assert.notEqual(check('1'.repeat(40)).status, 0);
});
