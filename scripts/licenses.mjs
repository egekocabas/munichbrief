#!/usr/bin/env node
// Offline verification. --write regenerates derived files; --record-inputs is an
// explicit maintainer acknowledgement after reviewing changed inputs.
import {readFileSync, writeFileSync, readdirSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {resolve, dirname} from 'node:path';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
process.chdir(root);
const manifestPath = 'internal/licensing/manifest.json';
const manifest = JSON.parse(readFileSync(manifestPath));
const hash = b => createHash('sha256').update(b).digest('hex');
const fail = message => { throw new Error(message); };
const inputs = ['go.mod', 'go.sum', 'package.json', 'package-lock.json', 'Dockerfile', 'LICENSE',
  ...readdirSync('internal/web/static').filter(n => !n.startsWith('.')).map(n => `internal/web/static/${n}`),
  ...readdirSync('internal/web/fonts').filter(n => /\.(ttf|txt)$/.test(n)).map(n => `internal/web/fonts/${n}`)].sort();
const fingerprints = Object.fromEntries(inputs.map(p => [p, hash(readFileSync(p))]));
if (process.argv.includes('--record-inputs')) {
  manifest.source_hashes = fingerprints;
  writeFileSync(manifestPath, JSON.stringify(manifest, null, 2)+'\n');
} else if (JSON.stringify(manifest.source_hashes) !== JSON.stringify(fingerprints)) {
  fail('Licence inputs changed. Review dependency/asset terms, update the inventory, then explicitly run --record-inputs --write.');
}
if (!/^\d{4}-\d{2}-\d{2}$/.test(manifest.credits_updated_at)) fail('Missing credits content-update date');
if (manifest.schema_version !== 1) fail('Unsupported licence inventory schema');
const ids = new Set();
const notices = manifest.notices.map(n => {
  if (!/^[a-z0-9-]+$/.test(n.id) || ids.has(n.id) || n.file !== `LICENSES/${n.id}.txt`) fail('Invalid/duplicate notice ID');
  ids.add(n.id);
  const bytes = readFileSync(n.file);
  if (!bytes.length || hash(bytes) !== n.sha256) fail(`Notice integrity failure: ${n.id}`);
  if (/^\s*(?:<!doctype html|<html)/i.test(bytes.toString())) fail(`HTML response stored as licence text: ${n.id}`);
  return {id:n.id, source:n.source, text:bytes.toString()};
});
const componentIDs = new Set();
const tags = new Set();
for (const c of manifest.components) {
  if (!c.id || componentIDs.has(c.id)) fail('Invalid/duplicate component ID');
  componentIDs.add(c.id);
  for (const key of ['name','version','source','license','kind','reviewed_at','review_status']) if (!c[key]) fail(`${c.id}: missing ${key}`);
  for (const key of ['source','artifact_source']) if (c[key] && (!c[key].startsWith('https://') || /(?:localhost|192\.168\.|127\.0\.)/.test(c[key]))) fail(`${c.id}: invalid public source`);
  if (!/^\d{4}-\d{2}-\d{2}$/.test(c.reviewed_at)) fail(`${c.id}: invalid review date`);
  if (!['reviewed','conditions','review_needed','excluded'].includes(c.review_status)) fail(`${c.id}: invalid review status`);
  if (c.kind !== 'model' && c.review_status !== 'reviewed') fail(`${c.id}: unreviewed shipped component`);
  if (!c.notices.length && !['model','asset'].includes(c.kind)) fail(`${c.id}: missing notices`);
  for (const id of c.notices) if (!ids.has(id)) fail(`${c.id}: unknown notice ${id}`);
  if (c.kind === 'model') {
    if (!c.model_tag || tags.has(c.model_tag) || !/^[a-f0-9]{64}$/.test(c.digest) || !c.upstream_revision || !c.artifact_revision) fail(`${c.id}: missing/duplicate exact model provenance`);
    tags.add(c.model_tag);
    if (c.public && ['excluded','review_needed'].includes(c.review_status)) fail(`${c.id}: unresolved model in public credits`);
  }
}
for (const file of inputs.filter(p => p.startsWith('internal/web/') && !p.endsWith('OFL.txt'))) {
  if (!manifest.components.some(c => c.files?.includes(file))) fail(`Asset has no reviewed owner: ${file}`);
}
for (const archive of manifest.source_archives ?? []) {
  if (!archive.file.startsWith('LICENSES/sources/') || archive.file.includes('..') || !componentIDs.has(archive.component)) fail('Invalid source archive');
  if (hash(readFileSync(archive.file)) !== archive.sha256) fail(`Source archive integrity failure: ${archive.file}`);
}
const docker = readFileSync('Dockerfile','utf8');
if (!docker.includes(manifest.container_base.reference) || !docker.includes(manifest.container_builder)) fail('Container image changed without a licence review');
const lock = JSON.parse(readFileSync('package-lock.json'));
for (const c of manifest.components.filter(c => c.npm)) {
  if (lock.packages[`node_modules/${c.npm}`]?.version !== c.version) fail(`npm version mismatch: ${c.npm}`);
}
// Resolve the already-installed toolchain before disabling its automatic
// download mechanism. This also supports Go's downloaded toolchain layout.
const goRoot = execFileSync('go',['env','GOROOT'],{encoding:'utf8',env:{...process.env,GOPROXY:'off'}}).trim();
for (const arch of ['amd64','arm64']) {
  const deps = execFileSync(resolve(goRoot,'bin/go'),['list','-buildvcs=false','-deps','-f','{{if .Module}}{{if not .Module.Main}}{{.Module.Path}}@{{.Module.Version}}{{end}}{{end}}','./cmd/munichbrief'],
    {encoding:'utf8',env:{...process.env,GOTOOLCHAIN:'local',GOPROXY:'off',GOSUMDB:'off',GOOS:'linux',GOARCH:arch,CGO_ENABLED:'0'}}).trim().split(/\s+/).filter(Boolean);
  for (const dep of new Set(deps)) if (!manifest.components.some(c => c.module && `${c.module}@${c.version}` === dep && c.locations.includes('binary'))) fail(`Unaccounted ${arch} linked module: ${dep}`);
}
const bundle = JSON.stringify({reviewed_at:manifest.reviewed_at, credits_updated_at:manifest.credits_updated_at, components:manifest.components, notices});
let markdown = '# Third-party notices\n\nGenerated from the reviewed licence inventory. Do not edit this file directly.\n\n';
markdown += 'MunichBrief project code uses MIT. Third-party software, fonts, models and data retain their own terms. Models and Ollama run separately and are not included in the application image. This inventory is not a live deployment list.\n\n';
for (const c of manifest.components) {
  markdown += `## ${c.name} — ${c.version}\n\n- Source: [upstream](${c.source})\n- Licence: ${c.license}\n- Included in: ${c.locations.join(', ')}\n- Reviewed: ${c.reviewed_at}\n`;
  for (const id of c.notices) markdown += `- [${id}](LICENSES/${id}.txt)\n`;
  if (c.conditions) markdown += `\n${c.conditions}\n`;
  for (const a of (manifest.source_archives ?? []).filter(a => a.component === c.id)) markdown += `\nCorresponding source: [${a.file.split('/').pop()}](${a.file}) · [upstream archive](${a.source})\n`;
  markdown += '\n';
}
const generated = {'internal/licensing/bundle.json':bundle+'\n','THIRD_PARTY_NOTICES.md':markdown.trimEnd()+'\n'};
for (const [path, contents] of Object.entries(generated)) {
  if (process.argv.includes('--write')) writeFileSync(path,contents);
  else if (readFileSync(path,'utf8') !== contents) fail(`Stale generated licence file: ${path}`);
}
console.log(`Verified ${manifest.components.length} components, ${notices.length} notice texts and ${tags.size} exact model artefacts for linux/amd64 and linux/arm64.`);
