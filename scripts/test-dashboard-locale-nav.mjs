// Unit regression for the merchant fast-navigation routing seam (BUG-LOCALE-NAV-02).
// Evaluates dashboard.js (definitions only) and calls initMerchantFastNavigation() directly,
// then drives the registered click/pointerover/focusin handlers with stub anchors.
// Locale links (lang query or hreflang) must never be prefetched or fast-navigated: a fetch
// would follow the 303 and let the middleware rotate the portal language cookie while the
// current document keeps its own locale, flipping the workspace language on the next partial.
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const file = process.env.ZH04_NAV_FILE || new URL('../views/assets/dashboard.js', import.meta.url);
const src = readFileSync(file, 'utf8');
const ORIGIN = 'http://127.0.0.1:3001';
const listeners = new Map();
const attrs = new Map();
const fetches = [];

const element = () => ({
  setAttribute() {}, removeAttribute() {}, getAttribute: () => null,
  classList: { contains: () => false, remove() {}, add() {}, toggle() {} },
  querySelectorAll: () => [], querySelector: () => null, closest: () => null,
  appendChild: (x) => x, insertBefore: (x) => x, removeChild: (x) => x, remove() {},
  contains: () => false, matches: () => false, cloneNode() { return element(); }, replaceWith() {}, remove() {},
  addEventListener() {}, removeEventListener() {}, dispatchEvent() {},
  getBoundingClientRect: () => ({ top: 0, left: 0, width: 0, height: 0, bottom: 0, right: 0 }),
  style: {}, dataset: {}, children: [], childNodes: [], firstChild: null, lastChild: null,
  innerHTML: '', textContent: '', value: '', checked: false, disabled: false, tagName: 'DIV', hidden: false, focus() {},
});
const hybrid = () => element();
Object.assign(hybrid, element());
let docLang = 'zh-CN';
const documentElement = Object.assign(element(), {
  getAttribute: (n) => (n === 'lang' ? docLang : (attrs.has(n) ? attrs.get(n) : null)),
  setAttribute: (n, v) => { attrs.set(n, v); },
});
Object.defineProperty(documentElement, 'lang', { get: () => docLang });
const langLinks = ['en', 'zh-CN'].map((hl) => {
  const node = element();
  node.getAttribute = (n) => (n === 'hreflang' ? hl : null);
  node.setAttribute = (n, v) => { if (n === 'href') node.href = v; };
  return node;
});
const body = Object.assign(element(), { classList: { contains: (c) => c === 'merchant-shell-body', remove() {}, add() {}, toggle() {} } });
const documentStub = new Proxy({
  addEventListener: (t, fn) => { const l = listeners.get(t) || []; l.push(fn); listeners.set(t, l); },
  querySelector: (sel) => (String(sel).includes('merchant-workbench') ? element() : null),
  querySelectorAll: (sel) => (String(sel).includes('hreflang') ? langLinks : []),
  getElementById: () => null, body, documentElement, dispatchEvent() {}, cookie: '',
  readyState: 'complete',
}, { get: (t, p) => (p in t ? t[p] : (typeof p === 'string' ? hybrid : undefined)) });
const winListeners = new Map();
const nav = { assigned: [], reloaded: 0, pushed: [] };
const sandbox = {
  document: documentStub, console, URL, Map, Date, setTimeout, clearTimeout,
  window: { location: { href: ORIGIN + '/merchant/dashboard', origin: ORIGIN, pathname: '/merchant/dashboard', search: '', assign: (u) => nav.assigned.push(String(u)), reload: () => { nav.reloaded++; } },
    history: { pushState: (a, b, u) => nav.pushed.push(String(u)) },
    addEventListener: (t, fn) => { const l = winListeners.get(t) || []; l.push(fn); winListeners.set(t, l); }, scrollTo() {} },
  fetch: (key, opts) => {
    fetches.push({ key, partial: (opts && opts.headers && opts.headers['X-Merchant-Navigation']) || null,
                   locale: (opts && opts.headers && opts.headers['X-Merchant-Locale']) || null, credentials: opts && opts.credentials });
    if (typeof globalThis.__nextResponse === 'function') return globalThis.__nextResponse(key, opts);
    return new Promise(() => {});
  },
  DOMParser: class { parseFromString() { return { querySelector: () => element(), title: 'x' }; } },
  CustomEvent: class { constructor() {} }, localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
  sessionStorage: { getItem: () => null, setItem() {}, removeItem() {} },
};
sandbox.globalThis = sandbox;
vm.runInNewContext(src, sandbox, { filename: 'dashboard.js' });
sandbox.initMerchantFastNavigation();
const bodyGuard = sandbox.initMerchantFastNavigation();

let failed = 0, checks = 0;
const assert = (name, ok, detail = '') => { checks++; if (!ok) { failed++; console.log(`FAIL ${name}${detail ? ' | ' + detail : ''}`); } else console.log(`PASS ${name}${detail ? ' | ' + detail : ''}`); };

assert('click/pointerover/focusin handlers registered', ['click', 'pointerover', 'focusin'].every((t) => (listeners.get(t) || []).length > 0), [...listeners.keys()].join(','));
assert('initialised flag set so a second call is a no-op', attrs.get('data-merchant-fast-nav-ready') === 'true' && (listeners.get('click') || []).length === 1, `flag=${attrs.get('data-merchant-fast-nav-ready')} clickHandlers=${(listeners.get('click') || []).length}`);

const anchor = (href, hreflang = false) => ({ href, target: '', hasAttribute: (n) => (n === 'download' ? false : n === 'hreflang' ? hreflang : false), classList: { contains: () => false }, closest: () => anchorSelf });
let anchorSelf = null;
function dispatch(type, href, hreflang) {
  anchorSelf = anchor(href, hreflang);
  const event = { button: 0, defaultPrevented: false, prevented: false, target: anchorSelf, preventDefault() { this.prevented = true; } };
  const before = fetches.length;
  for (const fn of listeners.get(type) || []) { try { fn(event); } catch (error) { console.log('handler error:', String(error).slice(0, 90)); } }
  return { prevented: event.prevented, fetched: fetches.slice(before) };
}
const positive = [
  ['workspace hop activity', ORIGIN + '/merchant/dashboard/activity', false],
  ['workspace hop users', ORIGIN + '/merchant/dashboard/users', false],
  ['workspace hop with page param', ORIGIN + '/merchant/dashboard/users?page=2', false],
];
for (const [name, href, hl] of positive) {
  const hover = dispatch('pointerover', href, hl);
  assert(`prefetch fetches ${name}`, hover.fetched.length === 1 && hover.fetched[0].key === new URL(href).pathname + new URL(href).search, JSON.stringify(hover.fetched));
  assert(`prefetch ${name} sends the partial header`, hover.fetched.length === 1 && hover.fetched[0].partial === 'partial' && hover.fetched[0].credentials === 'same-origin', JSON.stringify(hover.fetched[0] || {}));
  const click = dispatch('click', href, hl);
  assert(`click intercepts ${name} (preventDefault true)`, click.prevented === true, `prevented=${click.prevented}`);
}
const negative = [
  ['language switch ?lang=en', ORIGIN + '/merchant/dashboard/users?lang=en', true],
  ['language switch ?lang=zh-CN', ORIGIN + '/merchant/dashboard/activity?lang=zh-CN', true],
  ['language switch with other params', ORIGIN + '/merchant/dashboard/users?page=2&lang=en', true],
  ['language switch invalid value', ORIGIN + '/merchant/dashboard/users?lang=xx-INVALID', true],
  ['hreflang link without lang query', ORIGIN + '/merchant/dashboard/users', true],
];
for (const [name, href, hl] of negative) {
  const hover = dispatch('pointerover', href, hl);
  assert(`no prefetch for ${name}`, hover.fetched.length === 0, JSON.stringify(hover.fetched));
  const focus = dispatch('focusin', href, hl);
  assert(`no focus prefetch for ${name}`, focus.fetched.length === 0, JSON.stringify(focus.fetched));
  const click = dispatch('click', href, hl);
  // the click must not be intercepted; a fetch count of 0 can also mean the earlier hover
  // poisoned the 10s cache, which is exactly the defect, so only preventDefault is asserted here
  assert(`click not intercepted for ${name} (preventDefault false)`, click.prevented === false, `prevented=${click.prevented} fetched=${click.fetched.length}`);
}
// --- locale-scoped partial contract -------------------------------------------------
const flush = () => new Promise((r) => setTimeout(r, 0));
const workspace = ORIGIN + '/merchant/dashboard/activity?probe=1';
const before = fetches.length;
dispatch('click', workspace, false);
assert('partial request carries the document locale header', fetches.length > before && fetches[before].locale === 'zh-CN', JSON.stringify(fetches[before] || {}));
assert('partial request path excludes the locale from the cache key', fetches.length > before && !String(fetches[before].key).includes('zh-CN|'), fetches[before] && fetches[before].key);
assert('workspace links remain fast-navigated (partial count > 0)', fetches.slice(before).some((f) => f.partial === 'partial'), JSON.stringify(fetches.slice(before)));
// a response rendered in another locale must fall back to a real navigation, never inject DOM
globalThis.__nextResponse = () => Promise.resolve({ ok: true, headers: { get: (n) => (n === 'X-Merchant-Locale' ? 'en' : null) }, text: () => Promise.resolve('<html lang="en"></html>') });
const mismatchUrl = ORIGIN + '/merchant/dashboard/users?probe=2';
dispatch('click', mismatchUrl, false);
await flush(); await flush();
assert('locale-mismatched partial triggers a full navigation', nav.assigned.some((u) => u.includes('/merchant/dashboard/users?probe=2')), JSON.stringify(nav.assigned));
assert('locale-mismatched partial does not reload in place', nav.reloaded === 0, `reloaded=${nav.reloaded}`);
// a matching response renders and re-syncs both language links to the current page
globalThis.__nextResponse = () => Promise.resolve({ ok: true, headers: { get: (n) => (n === 'X-Merchant-Locale' ? 'zh-CN' : null) }, text: () => Promise.resolve('<html lang="zh-CN"></html>') });
dispatch('click', ORIGIN + '/merchant/dashboard/users?probe=3&foo=bar&foo=baz&page=2', false);
await flush(); await flush();
const hrefs = langLinks.map((l) => l.href || '');
assert('language links re-synced to the current page after a partial render', hrefs.every((h) => h && h.includes('/merchant/dashboard/users') && h.includes('page=2')), JSON.stringify(hrefs));
assert('re-synced links keep duplicate query parameters and set only lang', hrefs.every((h) => h.includes('foo=bar') && h.includes('foo=baz') && h.split('lang=').length === 2), JSON.stringify(hrefs));
// legacy history entries that still carry lang must do a real navigation
sandbox.window.location.href = ORIGIN + '/merchant/dashboard/users?lang=en';
sandbox.window.location.pathname = '/merchant/dashboard/users'; sandbox.window.location.search = '?lang=en';
for (const fn of winListeners.get('popstate') || []) fn({});
assert('popstate with a lang query performs a real navigation', nav.assigned.some((u) => u.includes('lang=en')), JSON.stringify(nav.assigned));
// focus-first prefetch must work with a cold cache (the old test could hit a warmed cache)
const coldBefore = fetches.length;
for (const fn of listeners.get('focusin') || []) { anchorSelf = anchor(ORIGIN + '/merchant/dashboard/domains', false); fn({ target: anchorSelf }); }
assert('focus alone prefetches a workspace link with a cold cache', fetches.length > coldBefore && fetches.slice(coldBefore).every((f) => f.partial === 'partial'), JSON.stringify(fetches.slice(coldBefore)));
// --- fail-closed response contract --------------------------------------------------
const resp = (loc) => () => Promise.resolve({ ok: true, headers: { get: (n) => (n === 'X-Merchant-Locale' ? loc : null) }, text: () => Promise.resolve('<html></html>') });
const clickFresh = async (path, responder) => {
  globalThis.__nextResponse = responder;
  const assignedBefore = nav.assigned.length;
  dispatch('click', ORIGIN + path, false);
  await flush(); await flush();
  return nav.assigned.length > assignedBefore;
};
assert('missing response locale falls back to a real navigation', await clickFresh('/merchant/dashboard/users?fc=1', resp(null)), JSON.stringify(nav.assigned.slice(-1)));
assert('malformed response locale falls back to a real navigation', await clickFresh('/merchant/dashboard/users?fc=2', resp('fr')), JSON.stringify(nav.assigned.slice(-1)));
// late response: the document locale changed while the partial was in flight
globalThis.__nextResponse = () => { docLang = 'en'; return resp('zh-CN')(); };
const beforeLate = nav.assigned.length;
dispatch('click', ORIGIN + '/merchant/dashboard/users?fc=3', false);
await flush(); await flush();
assert('a response that no longer matches the document locale falls back', nav.assigned.length > beforeLate, JSON.stringify(nav.assigned.slice(-1)));
docLang = 'zh-CN';
// the cache key is locale-scoped: the same URL under two document locales fetches twice
const sameURL = '/merchant/dashboard/domains?fc=4';
globalThis.__nextResponse = resp('zh-CN');
const n0 = fetches.length; dispatch('click', ORIGIN + sameURL, false); await flush(); await flush();
docLang = 'en';
globalThis.__nextResponse = resp('en');
dispatch('click', ORIGIN + sameURL, false); await flush(); await flush();
const scoped = fetches.slice(n0);
assert('same URL under two document locales issues two fetches (locale-scoped cache)', scoped.length === 2 && scoped[0].locale !== scoped[1].locale, JSON.stringify(scoped.map((f) => f.locale)));
docLang = 'zh-CN';
// a cache hit must not refetch within the TTL
const n1 = fetches.length; dispatch('click', ORIGIN + sameURL, false); await flush();
assert('cache hit avoids a second fetch for the same locale+URL', fetches.length === n1, `${fetches.length - n1} extra fetch(es)`);
// focus-first on a language link must never prefetch; an unknown lang is a normal navigation
const n2 = fetches.length;
for (const fn of listeners.get('focusin') || []) { anchorSelf = anchor(ORIGIN + '/merchant/dashboard/users?lang=en', true); fn({ target: anchorSelf }); }
for (const fn of listeners.get('pointerover') || []) { anchorSelf = anchor(ORIGIN + '/merchant/dashboard/users?lang=xx', true); fn({ target: anchorSelf }); }
assert('focus/pointerover on language links (valid or unknown lang) never prefetch', fetches.length === n2, JSON.stringify(fetches.slice(n2)));
console.log(`checks=${checks} failed=${failed} langFetches=${fetches.filter((f) => String(f.key).includes('lang=')).length}`);
console.log(failed ? `RESULT: ${failed} FAILURES` : 'RESULT: DASHBOARD-LOCALE-NAV OK');
process.exit(failed ? 1 : 0);
