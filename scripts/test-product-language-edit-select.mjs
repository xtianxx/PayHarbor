// 004 follow-up: reproducible, dependency-free check of the payment-link language path
// views/dealer/dashboard.html (select options + dataset) -> views/assets/dashboard.js
// (edit-modal population). It runs the shipped canonicalizer in an isolated vm context and
// proves that every dataset value the server can emit ends up on a real option, so saving an
// English link cannot silently rewrite it to Chinese.
//
// Run: node scripts/test-product-language-edit-select.mjs   (or bun)
import fs from 'node:fs';
import vm from 'node:vm';
import path from 'node:path';

const dashboard = fs.readFileSync(path.join(process.cwd(), 'views/dealer/dashboard.html'), 'utf8');
const script = fs.readFileSync(path.join(process.cwd(), 'views/assets/dashboard.js'), 'utf8');

let checks = 0, failures = 0;
function check(name, fn) {
  checks++;
  try {
    const problem = fn();
    if (problem) { failures++; console.log(`FAIL ${name}: ${problem}`); }
    else console.log(`PASS ${name}`);
  } catch (error) {
    failures++;
    console.log(`FAIL ${name}: threw ${error && error.message}`);
  }
}

// selectOptions returns the option values of the select with the given id, in document order.
function selectOptions(id) {
  const anchor = dashboard.indexOf(`id="${id}"`);
  if (anchor < 0) return [];
  const end = dashboard.indexOf('</select>', anchor);
  const segment = dashboard.slice(anchor, end < 0 ? undefined : end);
  return Array.from(segment.matchAll(/value="([^"]*)"/g)).map((match) => match[1]);
}

// loadCanonicalizer runs the shipped function in an isolated context.
function loadCanonicalizer() {
  const start = script.indexOf('function canonicalProductLanguage(value) {');
  if (start < 0) throw new Error('canonicalProductLanguage is not defined in dashboard.js');
  let depth = 0, end = start;
  for (let i = start; i < script.length; i++) {
    if (script[i] === '{') depth++;
    if (script[i] === '}') { depth--; if (depth === 0) { end = i + 1; break; } }
  }
  const context = vm.createContext({});
  vm.runInContext(`${script.slice(start, end)}; this.canonicalProductLanguage = canonicalProductLanguage;`, context, { filename: 'dashboard.js' });
  return context.canonicalProductLanguage;
}

const editOptions = selectOptions('product_edit_language');
const createOptions = selectOptions('product_language');

check('edit form offers exactly the supported languages', () => (editOptions.join(',') === 'zh-CN,en' ? null : `options=${editOptions.join(',')}`));
check('create form offers exactly the supported languages', () => (createOptions.join(',') === 'zh-CN,en' ? null : `options=${createOptions.join(',')}`));
check('edit form is populated from the data-product-language dataset', () => (dashboard.includes('data-product-language="{{.Language}}"') ? null : 'dataset binding missing'));

const canonicalProductLanguage = loadCanonicalizer();

// dataset values the view can emit, and what the select must end up holding.
const cases = [
  ['en', 'en'], ['EN', 'en'], ['en-US', 'en'], ['en_GB', 'en'],
  ['zh-CN', 'zh-CN'], ['ZH_CN', 'zh-CN'], ['zh', 'zh-CN'], ['zh-Hans', 'zh-CN'],
  ['tr', 'zh-CN'], ['TR', 'zh-CN'], ['tr-TR', 'zh-CN'], ['fr', 'zh-CN'], ['', 'zh-CN'], [null, 'zh-CN'],
];

for (const [dataset, expected] of cases) {
  check(`dataset ${JSON.stringify(dataset)} selects the ${expected} option`, () => {
    const value = canonicalProductLanguage(dataset);
    if (value !== expected) return `canonicalized to ${value}`;
    if (!editOptions.includes(value)) return `${value} is not an edit-form option`;
    return null;
  });
}

check('an english link keeps english when the form is submitted', () => {
  const submitted = canonicalProductLanguage('EN');
  return submitted === 'en' ? null : `would submit ${submitted}`;
});

check('the raw dataset value is what used to leave the select empty', () => {
  // Guards the reason this function exists: "EN"/"TR" (the pre-fix server values) are not
  // options, so forwarding them verbatim deselects the select and submits an empty language.
  const raw = 'EN';
  return editOptions.includes(raw) ? 'EN unexpectedly became an option; the canonicalizer may be redundant' : null;
});

check('dashboard.js no longer forwards the raw dataset value', () => (script.includes("language: canonicalProductLanguage(button.getAttribute('data-product-language')),") ? null : 'edit modal still assigns the raw dataset value'));

console.log(`\n${checks - failures}/${checks} checks passed`);
process.exit(failures === 0 ? 0 : 1);
