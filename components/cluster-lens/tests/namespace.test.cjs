const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

function setup(search) {
  const elements = new Map();
  const document = {getElementById(id) { if (!elements.has(id)) elements.set(id, {options: [], checked: false}); return elements.get(id); }, querySelector() { return {}; }};
  const source = fs.readFileSync(path.join(__dirname, '../frontend/app.js'), 'utf8').replace('\ninit();', '\n// Initialization is driven by the test.');
  const context = vm.createContext({document, window: {location: {search}}, URLSearchParams});
  vm.runInContext(source, context);
  return {context, elements};
}
test('selects the requested namespace and keeps it during empty snapshots', () => {
  const {context, elements} = setup('?namespace=shop');
  vm.runInContext('syncFilters({pods: [{namespace:"other"}]});', context);
  assert.equal(elements.get('namespaceFilter').value, 'shop');
  assert.equal(vm.runInContext('filterPods([{namespace:"other"},{namespace:"shop"}]).length', context), 1);
  vm.runInContext('syncFilters({pods: []});', context);
  assert.equal(elements.get('namespaceFilter').value, 'shop');
});
test('shows every namespace when opened without a filter', () => {
  const {context, elements} = setup('');
  vm.runInContext('syncFilters({pods: [{namespace:"one"},{namespace:"two"}]});', context);
  assert.equal(elements.get('namespaceFilter').value, 'all');
  assert.equal(vm.runInContext('filterPods([{namespace:"one"},{namespace:"two"}]).length', context), 2);
});
