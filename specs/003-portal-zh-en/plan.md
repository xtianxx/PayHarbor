# Implementation Plan: zh-CN/en Portal Localization

**Branch**: `003-portal-zh-en` | **Date**: 2026-10-12 | **Spec**: [spec.md](./spec.md)

**Input**: `specs/003-portal-zh-en/spec.md` (ZH-01 reconnaissance `/tmp/opencode/gateway-zh01/recon.md`)

## Summary

Make the English copy introduced by 002 the **source key** of a per-request locale lookup, add a `localization/` package holding the locale resolver plus the zh-CN catalog and the JS bundle builder, expose a GET-based header language switch that stores the choice in a **new** cookie (`gateway_portal_lang`), and teach `dashboard.js` to render its 66 strings from the injected bundle with locale-aware display sorting. Checkout (`views/gateway/*`, `views/assets/checkout.js`, `checkoutLanguage()`, `gateway_lang`) stays byte-frozen; protocol/machine surfaces are untouched.

## Technical Context

**Language/Version**: Go 1.25.4; server-rendered `html/template` through Fiber v3's html engine (`api/routes/routes.go:89-105`: `html.New("./views", ".html")`, `Reload/Debug(devMode)`; **no template funcs registered today**). Vanilla ES5-style JS in `views/assets/dashboard.js` (no bundler).

**Primary Dependencies**: none added. The catalog is Go data; the JS bundle is `json.Marshal` output (stdlib).

**Storage**: unchanged — no schema, no data write. `DealerPageData.Language` is product semantics and must not be reused as UI locale.

**Testing**: `go test ./...` in the `gotest` image (cached native layer), `views_english_test.go`/`views_test.go` (root, package `main`), `api/handlers/english_copy_test.go`, sidecar `--verify`, plus a render-level concurrency test and `-race` on the localization package, then HTTP + real-browser verification against the offline stack.

**Target Platform**: Linux container (`gateway-offline`), developer host WSL2 Ubuntu 24.04.

**Constraints**: copy + resolution only; protocol tokens frozen; checkout files byte-identical; no new dependency; no `.env`/auth/2FA/DB/fund change; no native Wallet Core rebuild (reuse Docker build cache).

**Scale/Scope**: ≈1,240 user-visible strings (templates ≈700 across 12 dealer templates + `invoice.html`, `dealer.go` 442, `txrescan.go` 19, `dashboard.js` 66, invoice 10), 8 touched production files + 1 new package + new tests. Verified seams: `DealerPageData` `api/handlers/dealer.go:132-278` built by `dealerPageData()` `:5694-5742`, `adminLoginPageData()` `:5744+`, `adminPageData()` `:6081-6114`; render sites `:1060,1075,1105,1113,1133,1172,1180,1229…1391,1487,1489`; `InvoicePageData` `api/handlers/payment.go:2181+` rendered at `:2258`; flash transport `redirectWithSuccess/Error` `dealer.go:5060-5068` → `applyFlash` `:5070+`.

## Constitution Check

| Principle | Status | Evidence |
| --- | --- | --- |
| Money-path / module boundaries (`docs/module-boundaries.md`) | PASS | Only display strings + a pure resolver; no repo/service/ledger touch; `normalizeAssetKey` semantics preserved by FR-009. |
| No new dependencies, no structural change to the UI | PASS | One new internal package, one header control, no CSS/Tailwind build change. |
| API/protocol stability (`docs/integration-guide.md`) | PASS | Routes, JSON keys, form names/ids, `data-*`, scopes, enums, statuses, logs frozen; checkout `Lang`/`gateway_lang` untouched. |
| Per-request isolation (no cross-request language bleed) | PASS (design) | Locale is a value in the per-request page data; template funcs take the locale explicitly; JS reads the injected bundle only, no global language mutation. |
| Observability unchanged | PASS | No log/message-code change. |
| Real verification | PASS (plan) | Dual-locale render matrix, concurrency test, sidecars, full suite, HTTP + browser switch-persistence checks. |

## Approach

### 1. Localization module (new package `localization/`)

```go
package localization

type Locale string

const (
    ZHCN Locale = "zh-CN"
    EN   Locale = "en"
    Default     = ZHCN
    CookieName  = "gateway_portal_lang" // distinct from checkout's gateway_lang
)

func Match(raw string) (Locale, bool)   // zh|zh-CN|zh-Hans -> ZHCN ; en|en-US -> EN ; else false
func Resolve(query, cookie string) Locale // explicit query > cookie > Default
func (l Locale) HTMLTag() string        // "zh-CN" | "en"  (for <html lang>)
func (l Locale) String() string

func T(l Locale, key string, args ...any) string // catalog lookup; missing -> key (English fallback)
func JSBundle(l Locale) template.JS              // json.Marshal of JS keys (HTML-escaped), template.JS
func Keys() []string; func JSKeys() []string     // for coverage tests
```

- Pure functions only; the catalog is read-only data (`catalog_zh.go`, `js_keys.go`) — no mutable package state, safe under concurrency.
- `#context` discriminator (`"Pending#status"`) for identical English text with different meanings; resolution order `key → "key#context"`.
- Go format verbs are preserved per entry; a test asserts verb multiset parity between key and translation.

### 2. Request seams

| Seam | Path | Change |
| --- | --- | --- |
| Switch redirect + cookie | new `api/middleware/portal_locale.go`, registered in `api/routes/routes.go` for the dealer/admin route set and the invoice route **only** | GET/HEAD with a recognized `lang` → set `gateway_portal_lang` (Path `/`, HTTPOnly, SameSite Lax, 1 year) → **303 See Other** to the same URL minus `lang`, all other parameters preserved (approved decision). Never on POST; never for `/checkout`, `/pay`, `/assets`, `/static`, `/api`. |
| Template func | `api/routes/routes.go:89-92` | `engine.AddFunc("t", localization.T)` (first Fiber/html-template API touch; verify against Fiber v3 docs before coding) |
| Page data | `DealerPageData` (`dealer.go:132-278`) | add `Locale localization.Locale`, `I18NJSON template.JS`, set once in `dealerPageData()` / `adminLoginPageData()` / `adminPageData()` so every page inherits them. These constructors take **no `*fiber.Ctx` today** (FR-015): the resolved locale must be passed in explicitly as a new parameter — never read from a package/global/context implicitly |
| Page titles | `dealerPageData(title, active)` `:5694-5742` | `data.Title = localization.T(loc, title)` — callers keep passing the English key |
| Invoice | `payment.go:2181+` `InvoicePageData`, `:2199-2258` handler, `invoiceStatusLabel` after `:2263` | add the same two fields; invoice-only hunks; no checkout-adjacent line touched |

### 3. Template seams

- `views/dealer/layout.html:2` → `<html lang="{{.Locale.HTMLTag}}">`; line 6 title suffix → `{{ t .Locale "Gateway Merchant Portal" }}`; the trusted payload goes into a **non-executing** element before line 10's deferred `dashboard.js`:
  `<script type="application/json" id="{{.I18NElementID}}">{{.I18NJSON}}</script>` (`BundleScriptElementID = "portal-i18n-data"`, content = `localization.JSBundle(loc)`).
- Inside `{{range}}`/nested blocks the locale is read from the root data (`{{ t $.Locale "…" }}`) — never from the range element (FR-016).
- Switcher link in `views/dealer/partials/header.html` (present for public/auth pages too), rendered exactly per `ui-guidance.md`, with hrefs built in Go (`SwitchURLZH`/`SwitchURLEN` computed per request from the current path + filtered query, `lang` stripped) — no JS required for switching, no form submission.
- Remaining in-scope templates: wrap visible literals with `{{ t .Locale "…" }}`; keep all `name=`/`id=`/`data-*`/action/href machine values verbatim.
- `views/invoice.html:2` → `lang="{{.Locale.HTMLTag}}"` + the same JSON element; copy wrapped; the switch is placed beside the invoice header status area per `ui-guidance.md` (A2 approved).

### 4. JS seams (`views/assets/dashboard.js`)

- Read the payload once at document start: `var PORTAL_I18N = JSON.parse(document.getElementById('portal-i18n-data').textContent);` → `{locale, strings}`; no `window` global language state, no `document.documentElement.lang` mutation, no storage.
- `function t(key, ...args)` looks up `PORTAL_I18N.strings[key]` (English fallback = the key) and formats multi-argument strings with a callback replacer (`String.replace(/%[sd]/g, cb)`) so every placeholder consumes its own argument and `$&`/`$1`/`%`/HTML in arguments are never re-interpreted (FR-017).
- Replace the 66 user-visible literals at their use sites (245, 257-291, 325, 1891-94, …); machine values and `data-*` reads untouched.
- Display locale: `localeCompare(…, 'en', …)` `:2298` and `normalize().toLocaleLowerCase('en-US')` `:2548` use `PORTAL_LOCALE`; **`normalizeAssetKey()` `:2422-27` (calls 958/976/1565/1590) keeps `tr-TR`**.
- Regenerate `views/assets/dashboard.js.fiber.br` (`go run ./scripts/brotli-sidecars views/assets/dashboard.js`), then `--verify`.

### 5. Test plan (dual matrix, machine contracts preserved)

1. `views_english_test.go` → rename/extend to a **dual-locale matrix**: keep the **29** existing cases (`englishRenderCases()`: 8 `publicView` + 9 `merchantPanel` + 6 `adminPanel` + 6 direct struct cases; static count — the authoritative number is `len(englishRenderCases())` / the `-v` output) plus `TestInvoiceRendersEnglishCopy`, and run each in `zh-CN` **and** `en`: assert `lang` attribute, per-locale anchors present, non-empty output, zero Turkish diacritics, and (zh case) absence of the case's pure-English anchor phrases to catch untranslated fallbacks. No existing case is deleted.
2. Root `views_test.go` keeps its structural/sidecar/gateway tests unchanged; `TestPortalDomHooksAndProtocolTokensFrozen` extends to the new template lines but keeps asserting the same frozen protocol literals.
3. `api/handlers/english_copy_test.go` → three rules: no Turkish display copy in `dealer.go`/`txrescan.go`; **CJK only inside `localization/`** (scan Go + templates); invoice labels resolve through `localization.T` while the checkout `TitleTR` token and the frozen legacy status tokens remain byte-identical.
4. New `localization/locale_test.go`: resolution priority and aliases, defaults, `HTMLTag`, junk/empty inputs, no-cookie-write signal; catalog coverage for every statically extracted key from Go (`T("…")`) and templates (`t .Locale "…"`), `%`-verb parity, `JSKeys()` ⊂ catalog; `JSBundle` round-trips through `json.Unmarshal`, contains no raw `<`, `>`, `&` and no `</script>`; concurrent `T`/`Resolve` under `-race`.
5. New render-level concurrency test (e.g. `localization_concurrency_test.go` in `main`): N≈200 alternating locales through template rendering assert each output's `lang`/anchors match the request's locale (no bleed).
6. Sidecar + full suite + stack update reuse the 002/003 procedure (`brotli-sidecars --verify`, `go build/vet/test ./...` in the `gotest` image, `docker compose … up -d --build` with cached layers).
7. Verification: HTTP switch persistence (`?lang=en` → 302 + cookie + English; `?lang=zh-CN` → Chinese; extra query params preserved; POST untouched), checkout `en`/`tr` regression, invoice `?lang=`, and a real-browser pass (switcher click, `lang` attr, `_portal_jwt` injection still present, no console errors). `browser-harness` CLI is not installed on this host; the harness's own Chromium/CDP `browser` tool is the browser path.

### 6. String delivery (security)

- Server-rendered copy: `localization.T` / `{{ t .Locale … }}` → plain `string`, auto-escaped by `html/template`; **never** `template.HTML`.
- JS bundle: `template.JS` whose value is `json.Marshal` output (`encoding/json` HTML-escapes `<`, `>`, `&` by default → `\u003c…`), so a `</script>` break-out is impossible; the bundle is built from the trusted catalog, never from user input; a test asserts no raw `<`/`&` and a JS `JSON.parse` smoke.

## Risks

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Default flips English → zh-CN (reverses 002's accepted default) | Merchant-facing behaviour change | Explicit supervisor decision (A1) before implementation |
| English-as-key drift (copy edit silently drops a zh entry) | English leaks into the Chinese page | Coverage test + per-case "no English anchor" assertions; fallback is always readable English |
| Missed strings (~1,240) | Mixed-language page | Mechanical wrapping + CJK-only-in-catalog test + per-case anchors; diacritic scan keeps guarding Turkish |
| Concurrency bleed | Wrong language per request | Per-request value only; explicit locale argument in templates; no package-level state; render-level concurrency test |
| `template.JS` misuse / XSS | Script injection | JSON with HTML escaping from the trusted catalog; tests for `</script>`/raw `<`; no user data in the bundle; no CSP present today (verified) — if CSP is added later, the inline bundle needs a nonce |
| CJK font/width rendering in fixed-width tables | Visual regressions | Browser verification phase; no CSS/Tailwind change planned (A4) |
| Flash message in the previous language | Inconsistent flash after a switch | Documented behaviour (FR-010): flash text is fixed at generation time |
| `gateway_portal_lang` reaching checkout | Checkout locale drift | Distinct cookie name, checkout never reads it, source-scan test; `gateway_lang` untouched |
| Sidecar drift | `TestFiberBrotliSidecarsMatchSources` fails | Regenerate with the committed tool, then `--verify` |
| Injection risk via switching URL construction | Open-redirect / XSS | Rebuild the URL from `c.Path()` + filtered query (same-host only), never from an attacker-supplied absolute URL; assert in tests |

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
| --- | --- | --- |
| New `localization` package + catalog | The user wants a switch with remembered choice and reproducible copy; hard-coding Chinese would make English unmaintainable | Hard-coded dual strings in ~1,240 places: doubles the literals and guarantees drift |
| English text as the lookup key | Zero-inventory migration of an already-English codebase; one catalog (zh) to maintain | Semantic keys require naming ~1,240 keys and maintaining an English catalog (2× work, drift) |
| One inline `<script>` bundle per in-scope page | Keeps JS copy server-authoritative and synchronously available without an extra request | Fetching `/assets/i18n.zh-CN.json` introduces async timing, caching and locale/session coupling |

## Phase Plan

- **Phase 0 (ZH-01)**: reconnaissance, baseline manifest, this plan — complete, no code.
- **Phase 1 (ZH-02)**: `localization/` package (resolver, catalog seed, bundle builder) + unit tests — **complete**, no UI change.
- **Phase 2**: Go/table seams: `routes.go` template func + locale middleware, page-data fields, titles, handler copy wrapping (`dealer.go`, `txrescan.go`, invoice-only `payment.go`).
- **Phase 3**: Templates (shell → merchant → admin → invoice) + header switch.
- **Phase 4**: `dashboard.js` copy + locale-aware display sorting + sidecar.
- **Phase 5**: Tests (dual matrix, coverage/freeze/concurrency) and full-suite run in the cached `gotest` image.
- **Phase 6**: Runtime HTTP + browser verification, evidence bundle, then supervisor review.

## Decisions (resolved by the supervisor, 2026-10-12)

- **A1 — confirmed**: portal/admin/invoice default is **zh-CN**; English stays selectable (flips 002's English default).
- **A2 — approved**: the invoice page also gets the switch, implemented strictly per `ui-guidance.md`.
- **A3 — corrected**: general **Deposit → 入账**; **充值** only where the copy explicitly means a customer top-up. Withdrawal 提现, Refund 退款, Sweep 归集, Payment link 支付链接, Invoice 发票, **API key API 密钥**, **API secret API 签名密钥**, **Webhook signing secret Webhook 签名密钥** (three distinct strings).
- **A4 — resolved**: no CSS/font change; narrow-screen defects go back to the designer.
- **A5 — confirmed**: portal, admin and invoice share the single cookie `gateway_portal_lang`.
- **A6 — confirmed**: `Accept-Language` is ignored (deterministic explicit resolution).
- **A7 — corrected**: `/api/v1` and all machine JSON (`code`/`schema`/keys) stay English/exact; portal-internal AJAX text that is displayed in the UI may be localized per request locale while keys/statuses stay unchanged; unknown downstream `err.Error()` text is preserved verbatim as a diagnostic; logs and webhook payloads are never localized.

## Stage status

- **ZH-01 (planning)**: complete — `recon.md`, baseline manifests, freeze fingerprints.
- **ZH-02 (core, this stage)**: complete — `localization/{locale.go,catalog_zh.go,bundle.go}` + `{locale_test.go,catalog_test.go,bundle_test.go}`; `gofmt` clean, `GOTOOLCHAIN=local go vet ./localization` exit 0, `GOTOOLCHAIN=local go test -race -count=1 ./localization` **17/17 PASS** (1.16 s). Catalog is a **representative seed** (~50 keys incl. the approved glossary and real dashboard.js strings); the full ≈1,200-string translation is **not** done.
- **ZH-03 (wiring)**: implemented for all four surfaces, verified at the light level only — **not accepted for final acceptance and not deployed** — middleware + template func + locale-explicit page data, 1,402 template `t` calls / 406 Go `t` calls / 61 JS `portalT` call sites, 1,248 catalog entries, coverage gate green (instrumented keys only: missing 0 — it does **not** prove un-instrumented copy is translated). Light host verification: `go build -tags walletcorefallback ./...`, `go vet`, `go test -race ./localization` (24/24), root render tests, `node --check`, standalone JS test, sidecar `--verify` (29/29), invoice render harness in both locales, XSS-probe escaping — all as observed tool output, with no raw exit-code logs (see `evidence/zh03-reconstructed-execution-record.md`). **Not done**: un-instrumented copy audit, terminology fix (Reserve wallet → 储备钱包), the zh half of the dual-locale matrix, concurrency/middleware-boundary/XSS tests, the CJK-only rule, and ZH-04's image build/full suite/runtime acceptance. **Not deployed** (running image is the previous English build). Budget overran ≈3 h against 45–60 min → future stages split into bounded tasks with stop-and-report. Full record: `/tmp/opencode/gateway-zh01/REPORT-RECONCILE.md`.
- **ZH-04 (build + acceptance)**: pending.

### 7. HTML engine concurrency gate (`api/viewengine`)

The upstream Fiber html engine (`github.com/gofiber/template/html/v3 v3.0.4`) is **not safe for concurrent renders**: `Render` copies a shared `*template.Template` outside any lock (`html.go:147`) and keeps the `{{embed}}` layout function in a shared func map (`html.go:167`). Under `-race` this surfaced as a data race in `html/template.escapeTemplate()` (lazy first-execution escaping) and, without `-race`, as `dealer/layout:19 <embed>: call of nil function`.

`api/viewengine.NewHTML(directory, development) fiber.Views` wraps the same upstream engine with **one mutex guarding both `Load` and `Render`**, delegating directly (no buffering, no writer replay, no swallowed errors) and preserving the upstream configuration (`".html"`, `Reload/Debug` by mode, `AddFunc("t", localization.T)`). The `{{embed}}` closure calls the inner engine, not the wrapper, so there is no re-entrant deadlock. `routes.go` builds the engine through this factory; **only HTML rendering is serialized** — every other request path stays fully concurrent.

**Cost, measured not assumed**: 200 simultaneous renders (mixed login / admin-networks / invoice layouts) ≈354 ms cold and ≈345 ms repeated; the `Reload(true)` development path (fixture templates, re-parse per request) ≈85 ms. The serialization is a real throughput trade-off, not a free fix; an upstream engine with per-call template state could remove the lock.

**Not verified**: `Load` is *not* transactional — a failure part-way through parsing leaves partial state (the adapter deliberately adds no buffering/rollback, matching upstream and the existing partial-output semantics).
