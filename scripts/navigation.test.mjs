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
    document, location, window, URL, TextEncoder, HTMLSelectElement: class {},
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
    change(field) {
      const event = new Event('change');
      Object.defineProperty(event, 'target', {value: field});
      document.dispatchEvent(event);
    },
    flushFrames() {
      frames.splice(0).forEach(callback => callback());
    },
  };
}

function savedReturn(timeline, restore = false) {
  return new Map([[storageKey, JSON.stringify({incident, timeline, scrollY: 1840, restore})]]);
}

for (const timeline of ['/en', '/en?page=2', '/en/search?q=bike&area=Schwabing&page=2', '/en/search?neighborhood=Maxvorstadt&neighborhood=Schwabing&period=week&page=2&view=incident&page_size=10']) {
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

test('invalid shortcut queries cannot restore a saved destination', () => {
  for (const query of ['period=month', 'period=today&period=week', 'neighborhood=', 'neighborhood=%00', Array(11).fill('neighborhood=A').join('&'), 'neighborhood=' + 'A'.repeat(201)]) {
    const storage = savedReturn('/en/search?' + query, true);
    loadPage(incident, storage);
    assert.equal(JSON.parse(storage.get(storageKey)), null, query);
  }
});

test('editing a date shortcut clears custom dates and editing custom dates clears the shortcut', () => {
  const page = loadPage('/en', new Map());
  const elements = {period: {value: 'today'}, from: {value: '2026-09-01'}, to: {value: '2026-09-30'}, date_field: {value: 'incident'}};
  const form = {elements, matches: selector => selector === '.reader-search-form'};
  page.change({name: 'period', form});
  assert.equal(elements.from.value, '');
  assert.equal(elements.to.value, '');
  assert.equal(elements.date_field.value, 'published');
  for (const name of ['from', 'to', 'date_field']) {
    elements.period.value = 'week';
    page.change({name, form});
    assert.equal(elements.period.value, '');
  }
});

test('neighborhood selection reports and clears the limit before form submission', () => {
  const page = loadPage('/en', new Map());
  const choices = Array.from({length: 11}, (_, index) => ({checked: true, value: 'Area ' + index, setCustomValidity(message) { this.error = message; }}));
  const form = {matches: selector => selector === '[data-neighborhood-limit]', querySelectorAll: () => choices, dataset: {neighborhoodLimit: 'Choose up to 10'}};
  const field = choices[10];
  field.form = form;
  page.change(field);
  assert.equal(field.error, 'Choose up to 10');
  field.checked = false;
  page.change(field);
  assert.ok(choices.every(choice => choice.error === ''));
});
