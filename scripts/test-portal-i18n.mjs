// TEST-01C2: reproducible, dependency-free checks for views/assets/portal-i18n.js.
// (This replaces the 17-check script whose log was lost with /tmp; it is a new persistent
// check, not a restored historical log.) Each fixture runs in an isolated vm context.
import fs from 'node:fs';
import vm from 'node:vm';
import path from 'node:path';

const source = fs.readFileSync(path.join(process.cwd(), 'views/assets/portal-i18n.js'), 'utf8');

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

// Build an isolated context. `element` is null to simulate a missing script element.
function load({ payloadText = null, element = undefined, withDocument = true, recordAccess = null } = {}) {
  const sandbox = {};
  if (withDocument) {
    const el = element === undefined && payloadText !== null
      ? { textContent: payloadText }
      : element;
    sandbox.document = {
      getElementById: (id) => { if (recordAccess) recordAccess.push('getElementById:' + id); return el; },
      get documentElement() { if (recordAccess) recordAccess.push('documentElement'); return {}; },
    };
  }
  const context = vm.createContext(sandbox);
  vm.runInContext(source, context, { filename: 'portal-i18n.js' });
  return context;
}

const json = (value) => JSON.stringify(value);
const enPayload = json({ locale: 'en', strings: { Copied: 'En copied', 'Raw: %s': 'Raw: %s' } });
const zhPayload = json({ locale: 'zh-CN', strings: { Copied: '已复制', 'Raw: %s': '原始：%s' } });

check('en payload locale + strings', () => {
  const ctx = load({ payloadText: enPayload });
  if (ctx.portalLocale() !== 'en') return `locale=${ctx.portalLocale()}`;
  if (ctx.portalT('Copied') !== 'En copied') return `text=${ctx.portalT('Copied')}`;
});

check('zh payload locale + strings', () => {
  const ctx = load({ payloadText: zhPayload });
  if (ctx.portalLocale() !== 'zh-CN') return `locale=${ctx.portalLocale()}`;
  if (ctx.portalT('Copied') !== '已复制') return `text=${ctx.portalT('Copied')}`;
});

check('payload is read once and cached', () => {
  const element = { textContent: enPayload };
  const ctx = load({ element });
  if (ctx.portalT('Copied') !== 'En copied') return 'first read wrong';
  element.textContent = zhPayload;
  if (ctx.portalT('Copied') !== 'En copied') return 'payload was re-read';
});

check('no document at all -> safe default, English fallback, no throw', () => {
  const ctx = load({ withDocument: false });
  if (ctx.portalLocale() !== 'zh-CN') return `locale=${ctx.portalLocale()}`;
  if (ctx.portalT('Copied') !== 'Copied') return `fallback=${ctx.portalT('Copied')}`;
});

check('missing element -> safe default', () => {
  const ctx = load({ element: null });
  if (ctx.portalLocale() !== 'zh-CN' || ctx.portalT('Copied') !== 'Copied') return 'not defaulted';
});

const malformed = [
  ['bad json', '{not json'],
  ['json number', '7'],
  ['json string', '"x"'],
  ['json null', 'null'],
  ['json array', '[]'],
  ['strings array', json({ locale: 'en', strings: ['a', 'b'] })],
  ['strings number', json({ locale: 'en', strings: 5 })],
  ['strings null', json({ locale: 'en', strings: null })],
];
for (const [name, text] of malformed) {
  check(`malformed payload (${name}) -> canonical default`, () => {
    const ctx = load({ payloadText: text });
    const locale = ctx.portalLocale();
    if (locale !== 'zh-CN' && locale !== 'en') return `locale=${locale}`;
    // no raw or malformed locale may survive, and the fallback stays the key
    if (String(locale).includes('garbage') || String(locale).includes('extra')) return `raw locale leaked: ${locale}`;
    if (ctx.portalT('Copied') === 'X') return 'strings from a malformed payload were accepted';
    if (ctx.portalT('Copied') !== 'Copied') return `fallback=${ctx.portalT('Copied')}`;
  });
}

// A payload whose strings are well-formed but whose locale is not canonical keeps the
// strings and falls back to the canonical default locale - never to the raw value.
for (const [name, text] of [
  ['locale en-garbage', json({ locale: 'en-garbage', strings: { Copied: 'X' } })],
  ['locale zh-CN-extra', json({ locale: 'zh-CN-extra', strings: { Copied: 'X' } })],
  ['locale raw zh', json({ locale: 'zh', strings: { Copied: 'X' } })],
  ['locale number', json({ locale: 5, strings: { Copied: 'X' } })],
]) {
  check(`non-canonical locale (${name}) is canonicalized, strings kept`, () => {
    const ctx = load({ payloadText: text });
    if (ctx.portalLocale() !== 'zh-CN') return `locale=${ctx.portalLocale()}`;
    if (ctx.portalT('Copied') !== 'X') return `strings lost: ${ctx.portalT('Copied')}`;
  });
}

check('strings keep only string values', () => {
  const text = json({ locale: 'en', strings: { ok: 'yes', badNumber: 3, badObject: { a: 1 }, badNull: null, badArray: [] } });
  const ctx = load({ payloadText: text });
  if (ctx.portalT('ok') !== 'yes') return 'string value dropped';
  for (const key of ['badNumber', 'badObject', 'badNull', 'badArray']) {
    if (ctx.portalT(key) !== key) return `non-string value accepted for ${key}`;
  }
});

check('unknown key falls back to the key', () => {
  const ctx = load({ payloadText: zhPayload });
  if (ctx.portalT('Not a key') !== 'Not a key') return `got ${ctx.portalT('Not a key')}`;
});

check('formatting: multiple %s and %d consume their own argument', () => {
  const ctx = load({ payloadText: json({ locale: 'en', strings: { k: '%s / %d / %s' } }) });
  if (ctx.portalT('k', 'a', 2, 'c') !== 'a / 2 / c') return ctx.portalT('k', 'a', 2, 'c');
  if (ctx.portalT('k', 'a') !== 'a / %d / %s') return `missing args: ${ctx.portalT('k', 'a')}`;
});

check('literal percent and %s inside arguments are not re-interpreted', () => {
  const ctx = load({ payloadText: json({ locale: 'en', strings: { k: 'v=%s', plain: '100% done' } }) });
  if (ctx.portalT('plain') !== '100% done') return ctx.portalT('plain');
  const hostile = '$& $1 %s <b>x</b>';
  if (ctx.portalT('k', hostile) !== 'v=' + hostile) return ctx.portalT('k', hostile);
});

check('U+2028 / U+2029 survive a JSON round-trip', () => {
  const line = 'a\u2028b\u2029c';
  const raw = '{"locale":"en","strings":{"k":"' + line + '"}}';
  const ctx = load({ payloadText: raw });
  if (ctx.portalT('k') !== line) return JSON.stringify(ctx.portalT('k'));
});

check('module never touches lang, navigator, cookies or storage', () => {
  const access = [];
  const sandbox = {
    document: {
      getElementById: () => ({ textContent: enPayload }),
      get documentElement() { access.push('documentElement'); return {}; },
      get cookie() { access.push('cookie'); return ''; },
    },
    get navigator() { access.push('navigator'); return {}; },
    get localStorage() { access.push('localStorage'); return {}; },
    get sessionStorage() { access.push('sessionStorage'); return {}; },
  };
  vm.runInContext(source, vm.createContext(sandbox), { filename: 'portal-i18n.js' });
  vm.runInContext('portalT("Copied"); portalLocale();', sandbox);
  return access.length ? `touched ${access.join(',')}` : null;
});

check('no eval / innerHTML / document.write in the runtime', () => {
  for (const needle of ['eval(', 'innerHTML', 'document.write', 'new Function']) {
    if (source.includes(needle)) return `source contains ${needle}`;
  }
});

console.log(`\n${checks - failures}/${checks} checks passed`);
console.log(`PORTAL-I18N-CHECKS ${failures === 0 ? 'OK' : 'FAILED'}`);
process.exit(failures === 0 ? 0 : 1);
