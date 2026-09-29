import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {test} from 'node:test';
import {runInNewContext} from 'node:vm';

const source = readFileSync(new URL('../internal/web/static/navigation.js', import.meta.url), 'utf8');
const storageKey = 'munichbrief-timeline-return';
const incident = '/en/incidents/42';

function loadPage(path, storage) {
  const document = new EventTarget();
  document.documentElement = {classList: {add() {}}};
  document.querySelector = () => null;
  document.referrer = '';
  const location = new URL(path, 'https://munichbrief.de');
  let reloads = 0;
  location.reload = () => { reloads++; };
  const window = new EventTarget();
  const scrolls = [];
  window.scrollTo = ({top}) => scrolls.push(top);
  const frames = [];
  runInNewContext(source, {
    document, location, window, URL,
    sessionStorage: {
      getItem: key => storage.get(key) ?? null,
      setItem: (key, value) => storage.set(key, value),
    },
    performance: {getEntriesByType: () => [{type: 'reload'}]},
    requestAnimationFrame: callback => frames.push(callback),
  });
  return {
    location, scrolls,
    get reloads() { return reloads; },
    emit(name) {
      const event = new Event(name, {cancelable: true});
      document.dispatchEvent(event);
      return event;
    },
    flushFrames() {
      frames.splice(0).forEach(callback => callback());
    },
  };
}

function savedReturn(timeline, restore = false) {
  return new Map([[storageKey, JSON.stringify({incident, timeline, scrollY: 1840, restore})]]);
}

for (const timeline of ['/en', '/en?page=2', '/en/search?q=bike&area=Schwabing&page=2']) {
  test(`browser Back restores the saved position after reloading ${timeline}`, () => {
    const storage = savedReturn(timeline);
    const detail = loadPage(incident, storage);
    // History traversal changes the URL before HTMX handles the cache miss.
    detail.location.href = new URL(timeline, detail.location).href;
    assert.equal(detail.emit('htmx:historyCacheMiss').defaultPrevented, true);
    assert.equal(detail.reloads, 1);

    const listing = loadPage(timeline, storage);
    listing.emit('DOMContentLoaded');
    listing.flushFrames();
    assert.deepEqual(listing.scrolls, [1840]);
    assert.equal(JSON.parse(storage.get(storageKey)).restore, false);
    listing.emit('htmx:afterSettle');
    listing.flushFrames();
    assert.deepEqual(listing.scrolls, [1840], 'restoration is consumed once');
  });
}

test('other history destinations do not inherit the timeline position', () => {
  for (const path of ['/en?page=3', '/en/incidents/42', '/en/about']) {
    const storage = savedReturn('/en?page=2');
    const page = loadPage(path, storage);
    page.emit('htmx:historyCacheMiss');
    const reloaded = loadPage(path, storage);
    reloaded.emit('DOMContentLoaded');
    reloaded.flushFrames();
    assert.deepEqual(reloaded.scrolls, []);
    assert.equal(JSON.parse(storage.get(storageKey)).restore, false);
  }
});

test('ordinary listing reloads do not force the old incident position', () => {
  const page = loadPage('/en', savedReturn('/en'));
  page.emit('DOMContentLoaded');
  page.flushFrames();
  assert.deepEqual(page.scrolls, []);
});

test('history without saved return state still reloads safely', () => {
  const page = loadPage('/en', new Map());
  assert.equal(page.emit('htmx:historyCacheMiss').defaultPrevented, true);
  assert.equal(page.reloads, 1);
});
