# Implementation Plan: Buyer-Flow Locale Unification (zh-CN default, English retained)

**Branch**: `004-buyer-locale-zh-en` (not created — the working tree carries large uncommitted supervisor work and must not be switched; artifacts live under `specs/004-buyer-locale-zh-en/`)

**Date**: 2026-10-15 | **Spec**: [spec.md](./spec.md)

**Baseline (read-only, taken before any edit)**: `/tmp/opencode/baseline-20261015T095207Z/` — `HEAD.txt`, `status-short.txt`, `tracked-diff.patch` (+ sha256), `tracked-diff-stat.txt`, `snapshot-targets.tgz` (views, localization, api/handlers, api/routes, static). Mode 0700, no `.env`/credentials, no full `docker inspect`/`config` output.

## Summary

Delete the buyer flow's private en/tr language system and route it through the existing `localization` package: one locale type, one priority order (`lang` query > `gateway_portal_lang` > legacy `gateway_lang` > `zh-CN`), one catalog (English source text as key), one server-rendered JSON bundle for browser copy. Turkish disappears from templates, handler copy, browser assets, the language switch, the product-language select and the stored default. No new dependency, no layout change, no protocol change.

## Technical Context

**Language/Version**: Go 1.26.5 toolchain via the project image; server-rendered `html/template` through `api/viewengine` (`t` template func = `localization.T`), vanilla ES5-style JS (`views/assets/*.js`, no bundler).

**Primary Dependencies**: none added. Catalog is Go data, bundle is `encoding/json` output.

**Storage**: no schema change; `Product.Language` default becomes `zh-CN` (new rows only), no row rewrite.

**Testing**: focused package runs inside the existing `gateway-offline:test` image (no host toolchain, no wallet-core rebuild): `docker run --rm gateway-offline:test go test -count=1 ./localization ./api/handlers ./api/middleware`; root-package view tests via the same image; `scripts/brotli-sidecars --verify`; browser checks through the browser harness against the offline stack.

**Target Platform**: Linux container (`gateway-offline`), host WSL2.

**Constraints**: no push/commit; no `.env`, secret or credential read-out; `GATEWAY_OFFLINE=true` retained; no RPC or money worker enabled; `_reserve_` and `shop.test` credentials/notification URLs untouched.

**Scale**: ~250 buyer strings across 4 templates + 1 browser asset + 1 handler file; 1 package extension + 1 catalog surface + ~15 production files touched.

## Constitution Check

| Principle | Status | Evidence |
| --- | --- | --- |
| Money-path boundaries untouched | PASS | Only display copy, locale resolution and the product *language* default; no repo/service/ledger/status logic changes. |
| No new dependencies, no UI restructure | PASS | Reuses `localization` + existing language-control markup/CSS. |
| Protocol stability | PASS | Routes, JSON keys, form names, `data-*`, enums, HTTP statuses, webhook payloads, logs frozen (FR-012). |
| Per-request isolation | PASS | `BuyerLocale`/`T` are pure functions; no package-level locale state; the bundle is per-response. |
| Real verification | PASS (plan) | Dual-locale render tests, journey/regression tests, Turkish scan, concurrency + hostile-input tests, sidecar gate, deployed HTTP + browser evidence. |

## Approach

### 1. Localization package (extend, do not fork)

```go
// localization/locale.go
const LegacyCheckoutCookieName = "gateway_lang" // read-only legacy input

// BuyerLocale resolves the buyer-flow locale: recognized lang query, then the
// canonical gateway_portal_lang cookie, then the recognized legacy gateway_lang
// cookie (en/zh only — legacy "tr" is unrecognized), then Default.
func BuyerLocale(queryLang, portalCookie, legacyCookie string) Locale

// NormalizeLanguage canonicalizes a stored/requested product language to the
// supported set; blank, legacy "tr" and anything unrecognized become Default.
func NormalizeLanguage(raw string) Locale
```

- `catalog_zh_buyer.go` (new) holds `buyerZH`: checkout/pay/result/buyer-handler keys.
- `catalog_zh_browser.go`: `jsCatalogKeys` gains the buyer browser keys (checkout.js, pay.html inline script).
- Unused `"Turkish"` catalog entry is removed.

### 2. Middleware (one recording rule for every user-visible path)

`api/middleware/portal_locale.go`: `IsPortalLocalePath` gains `/checkout` (and children) and `/payment-links/`; GET/HEAD + recognized `lang` → write `gateway_portal_lang`, strip `lang`, redirect same-origin. POST mutations stay untouched. Result: the switch records the choice for the whole buyer journey, and the canonical URL never keeps a stale `lang`.

### 3. Buyer handler plumbing (`api/handlers/payment.go`, `api/handlers/dealer.go`)

- `checkoutLanguage` → `buyerLocale(c)` returning `localization.Locale` (query → canonical cookie → legacy cookie → default).
- Delete `normalizeLanguage` (en/tr) and its callers; delete `LangTRURL`/`LangENURL`; add `SwitchURLZH`/`SwitchURLEN` built with `localization.SwitchURL(current path, query, locale)` (drop `lang` from internally generated URLs — the cookie carries the choice).
- Page data for checkout/pay/result: `.Locale`, `.I18NJSON`, `.I18NElementID`, `.SwitchURLZH`, `.SwitchURLEN`; `.IsEnglish`/`.Lang`/`LangTRURL`/`LangENURL` removed.
- Handler copy through `localization.T(loc, …)`: `checkoutPayerState` (titles/bodies), `checkoutResultStatusLabel`, `checkoutReceiptRefs`, `renderPaymentError` titles/messages, invoice title/status labels.
- Payment-link entry: normalize stored product language, honor recognized `lang`, write the canonical cookie, redirect to the bare checkout path.

### 4. Templates (`views/gateway/{checkout,pay,payment_result,sample_crypto-checkout}.html`)

- `<html lang="{{.Locale.HTMLTag}}">`; every `.IsEnglish` branch collapses to a single `{{ t .Locale "English source" }}`.
- Language control keeps its markup/classes; two entries (`zh-CN`, `en`) with localized `title`/`aria-label`, `lang`/`hreflang`, exactly one `aria-current="true"`, GET links only.
- Buyer pages embed the bundle element and load `/assets/portal-i18n.js` before their own script.
- No copy, layout or `data-*` hook is otherwise changed; amounts/assets/addresses untouched.

### 5. Browser assets

- `views/assets/checkout.js`: copy comes from `portalT(...)` (bundle), the `document.documentElement.lang === 'tr'` switch and the inline en/tr tables are deleted.
- `pay.html` inline status script: en/tr literals → `portalT(...)` keys; terminal/pending/partial statuses keep their branch logic and classes.
- `.fiber.br` sidecars regenerated for every changed asset (`go run ./scripts/brotli-sidecars views/assets/checkout.js …`).

### 6. Product language (merchant-facing)

- `models/product.go` GORM default `tr` → `zh-CN`; `repositories/product_repo.go` blank → `localization` default; `api/handlers/dealer.go` + `api/handlers/v1api.go` normalize through `localization.NormalizeLanguage`.
- `views/dealer/dashboard.html` selects offer exactly `zh-CN` + `en` with localized labels; `views/assets/dashboard.js` fallback `'tr'` → `'zh-CN'`.
- No historical row is rewritten; legacy `tr` products resolve to Chinese at display time (FR-011).

### 7. Tests (contract migrations are explicit)

| Test | Change |
| --- | --- |
| `api/handlers/payment_test.go` (switcher markup, Turkish result copy) | Turkish assertions → zh-CN equivalents; explicit `en` coverage retained |
| `api/handlers/english_copy_test.go` ("checkout Turkish copy must remain") | contract inverted: no Turkish display copy anywhere, checkout copy catalog-instrumented |
| `views_english_test.go` (tr branches) | dual-locale zh/en equivalents |
| `api/middleware/portal_locale_test.go` (checkout excluded) | checkout/payment-links now in scope; POST still untouched |
| `localization/locale_test.go` (`tr` rejected) | unchanged + new `BuyerLocale`/`NormalizeLanguage` cases |
| new `localization/buyer_*_test.go` | priority order, legacy cookie, `zh-Hans`/`en-US` aliases, third-language fallback |
| new buyer render tests | evidence A (cookie-less + `Accept-Language: en` → zh-CN), B (en journey), C (Turkish scan in both locales), D (concurrent sessions) |
| `views_test.go` (`TestCheckoutAssetsKeepCopyFallbackAndReadableStatuses`) | updated to the bundle-driven contract |

No test is deleted to reach green; assertions migrate with the contract and the explicit-English path keeps its coverage.

### 8. Docs, deployment, verification

- Locale section of `docs/developer-guide.md` (+ offline dev doc if it states the checkout exception) updated: one mechanism, zh-CN default, legacy cookie read-only, Turkish removed.
- Deploy with the offline stack files only: `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml -f deploy/compose/offline/webhook-test.compose.yml up -d --build gateway`; postgres, volume, loopback, webhook-test and the internal network are preserved; `GATEWAY_OFFLINE=true` stays in force.
- Evidence recorded under `.cache/agent-evidence/004-buyer-locale-zh-en/` (test logs, coverage report, Turkish scan, HTTP/browser checks).

## Risks

| Risk | Mitigation |
| --- | --- |
| SSRF/redirect behaviour of the extended middleware | Reuses the accepted 003 middleware unchanged; hostile-input tests extended to buyer paths. |
| Double redirects from `lang`-bearing internal URLs | Internally generated URLs drop `lang`; only the switch link carries it. |
| Stale `.fiber.br` served to browsers | Sidecar regeneration + `TestFiberBrotliSidecarsMatchSources`. |
| Client/server locale disagreement (flash-back) | Bundle is server-rendered per request; no client-side locale derivation. |
| Legacy data/cookie values leaking Turkish into the UI | Unrecognized values resolve to the default at every entry point; covered by tests C. |
