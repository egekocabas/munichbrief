import {test, after} from 'node:test';
import assert from 'node:assert/strict';
import {cpSync, mkdtempSync, readFileSync, writeFileSync, rmSync, mkdirSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join, resolve, dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {spawnSync} from 'node:child_process';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const fixture = mkdtempSync(join(tmpdir(), 'munichbrief-licence-tests-'));
for (const path of ['go.mod','go.sum','package.json','package-lock.json','Dockerfile','LICENSE','LICENSES','THIRD_PARTY_NOTICES.md','internal','cmd']) cpSync(join(root,path), join(fixture,path), {recursive:true});
mkdirSync(join(fixture,'scripts'));
cpSync(join(root,'scripts/licenses.mjs'), join(fixture,'scripts/licenses.mjs'));
after(() => rmSync(fixture,{recursive:true,force:true}));
function rejects(path, change, pattern) {
  const target = join(fixture,path), original = readFileSync(target);
  try {
    writeFileSync(target, change(original.toString()));
    const result = spawnSync(process.execPath,[join(fixture,'scripts/licenses.mjs'),'--check'],{encoding:'utf8',env:process.env});
    assert.notEqual(result.status,0,'Invalid inventory passed');
    assert.match(result.stderr,pattern);
  } finally {writeFileSync(target,original);}
}
function manifest(change, pattern) {
  rejects('internal/licensing/manifest.json', text => {const m=JSON.parse(text);change(m);return JSON.stringify(m);},pattern);
}
test('changed dependency inputs need a review', () => rejects('go.sum', s=>s+'\n', /Licence inputs changed/));
test('changed original texts fail integrity checks', () => rejects('LICENSES/munichbrief-mit.txt', s=>s+'modified', /Notice integrity failure/));
test('unreviewed shipped components fail', () => manifest(m=>{m.components[0].review_status='review_needed';}, /unreviewed shipped component/));
test('unknown notice identifiers fail', () => manifest(m=>{m.components[0].notices.push('missing');}, /unknown notice/));
test('unresolved models cannot become public credits', () => manifest(m=>{m.components.find(c=>c.kind==='model'&&c.review_status==='review_needed').public=true;}, /unresolved model in public credits/));
test('missing artefact provenance fails', () => manifest(m=>{m.components.find(c=>c.kind==='model').digest='';}, /exact model provenance/));
test('missing compiled module coverage fails', () => manifest(m=>{m.components.find(c=>c.module==='modernc.org/sqlite').locations=[];}, /Unaccounted amd64 linked module/));
test('stale generated output fails', () => rejects('THIRD_PARTY_NOTICES.md', s=>s+'stale', /Stale generated licence file/));
