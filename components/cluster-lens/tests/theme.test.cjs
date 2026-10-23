const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
function setup(saved, dark = false, blocked = false) {
  const events = {};
  const attrs = {};
  const media = {matches: dark, addEventListener: (_, callback) => {events.media = callback;}};
  const button = {setAttribute: (key, value) => {attrs[key] = value;}, addEventListener: (_, callback) => {events.click = callback;}};
  const root = {dataset: {}, style: {}};
  const window = {localStorage: {getItem: () => {if (blocked) throw Error('blocked'); return saved;}, setItem: (_, value) => {if (blocked) throw Error('blocked'); saved = value;}}, matchMedia: () => media, addEventListener: (name, callback) => {events[name] = callback;}, dispatchEvent: () => {}};
  const document = {documentElement: root, getElementById: () => button, addEventListener: (_, callback) => {events.loaded = callback;}};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../frontend/theme.js'), 'utf8'), {window, document, CustomEvent: class {}});
  events.loaded();
  return {events, attrs, media, root, getSaved: () => saved, setSaved: value => {saved = value;}};
}
test('restores the saved theme and persists accessible toggle changes', () => {
  const value = setup('dark');
  assert.equal(value.root.dataset.theme, 'dark');
  assert.equal(value.attrs['aria-label'], 'Use light mode');
  value.events.click();
  assert.equal(value.root.dataset.theme, 'light');
  assert.equal(value.getSaved(), 'light');
  assert.equal(value.attrs['aria-pressed'], 'false');
});
test('follows system changes until an explicit preference is selected', () => {
  const value = setup(null, true);
  assert.equal(value.root.dataset.theme, 'dark');
  value.media.matches = false;
  value.events.media();
  assert.equal(value.root.dataset.theme, 'light');
  value.events.click();
  value.events.media();
  assert.equal(value.root.dataset.theme, 'dark');
});
test('synchronizes theme changes from another same-origin tab', () => {
  const value = setup('light');
  value.setSaved('dark');
  value.events.storage({key: 'kubephos-theme'});
  assert.equal(value.root.dataset.theme, 'dark');
});
test('works when preference storage is unavailable', () => {
  const value = setup(null, true, true);
  assert.equal(value.root.dataset.theme, 'dark');
  value.events.click();
  assert.equal(value.root.dataset.theme, 'light');
});
