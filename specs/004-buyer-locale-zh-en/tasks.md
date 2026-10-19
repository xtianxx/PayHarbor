---
description: "Task breakdown for the buyer-flow locale unification (zh-CN default, English retained, Turkish removed)"
---

# Tasks: Buyer-Flow Locale Unification (004)

**Input**: `specs/004-buyer-locale-zh-en/{spec.md,plan.md}`
**Baseline (read-only)**: `/tmp/opencode/baseline-20261015T095207Z/` (`HEAD.txt`, `status-short.txt`, `tracked-diff.patch` + sha256, `snapshot-targets.tgz`)
**Evidence**: `.cache/agent-evidence/004-buyer-locale-zh-en/`

Legend: `[x]` done with evidence · `[~]` partial · `[ ]` open

## Phase 0 — Reconnaissance and contracts

- [x] **T401** Recon read-only of the two language systems (portal `localization` vs checkout en/tr): call sites, cookie names, persisted `Product.Language`, template branches, browser copy, test inventory.
- [x] **T402** Spec/plan authored for 004 (`spec.md`, `plan.md`); documents that this supersedes 002's English-default rule for the checkout and extends 003 to the buyer flow. Spec Kit's `create-new-feature.sh` was **not** run: it creates and switches a git branch, which would disturb the supervisor's uncommitted tree.

## Phase 1 — Locale core (`localization`)

- [x] **T403** `BuyerLocale(query, portalCookie, legacyCookie)`, `BuyerLocaleForProduct(…, productLanguage)` and `NormalizeLanguage(raw)`; `LegacyCheckoutCookieName`; package doc updated (checkout is no longer out of scope). Pure functions, no package state.
- [x] **T404** `buyerZH` catalog surface (`localization/catalog_zh_buyer.go`) registered in `catalogZH`; buyer browser keys listed in `jsCatalogKeys`; retired `"Turkish"` entries removed from the dashboard/admin catalogs.
- [x] **T405** Unit tests: priority order, legacy cookie, retired `tr` and third languages, product-language default, stored-value normalization, buyer browser keys covered.

## Phase 2 — Server side

- [x] **T406** `api/handlers/payment.go`: `checkoutLanguage` → `buyerLocale`; no buyer cookie is written by handlers; internal URLs drop `lang` (`checkoutURL`); switch links via `localization.SwitchURL`; page data carries `Locale`/`I18NJSON`/`I18NElementID`/`SwitchURLZH`/`SwitchURLEN`.
- [x] **T407** Buyer copy through the catalog: `checkoutPayerState` (titles/bodies), `checkoutResultStatusLabel`, `checkoutReceiptRefs`, `renderPaymentError`, cancel/success result copy, payer errors.
- [x] **T408** `api/handlers/dealer.go`: payment-link entry resolves and records the journey locale, drops `?lang=` from the redirect and the legacy cookie write; `normalizeLanguage` removed; `renderPaymentLinkError` localizes title/message/status; admin test-deposit paths use `checkoutURL`.
- [x] **T409** `api/handlers/v1api.go` + `models/product.go` + `repositories/product_repo.go`: product/payment-link language normalizes to the supported set, column default becomes `zh-CN` (`models.DefaultProductLanguage`); no historical row rewritten.
- [x] **T410** `api/middleware/portal_locale.go`: buyer paths (`/checkout/…`, `/payment-links/…`) join the locale middleware; POST mutations untouched.

## Phase 3 — Views and browser assets

- [x] **T411** `views/gateway/checkout.html`, `pay.html`, `payment_result.html`: locale-driven `<html lang>`, canonical two-locale language control (existing `crypto-lang-*` markup, `cn.svg`/`gb.svg`), all `.IsEnglish` branches collapsed to `{{ t .Locale … }}`, bundle element + `portal-i18n.js` loaded before `checkout.js`.
- [x] **T412** `views/assets/checkout.js`: copy from the bundle (`buyerCopy`), no `document.documentElement.lang === 'tr'` branch.
- [x] **T413** `views/dealer/dashboard.html`, `views/dealer/admin_dashboard.html`, `views/assets/dashboard.js`: language selects offer exactly `zh-CN`/`en`, stale `lang=tr` form field removed, JS fallback default updated.
- [x] **T414** `.fiber.br` sidecars regenerated for every changed asset; `--verify` reports 0 mismatches.

## Phase 4 — Contract migration of existing tests

- [x] **T415** `payment_test.go`: Turkish assertions replaced by zh-CN equivalents (canceled/paid/success/donation paths), `IsEnglish`/`LangTRURL` data maps migrated, language-switcher markup contract re-pointed at the two supported locales, redirect tests migrated to "locale lives in the cookie, URLs carry no `lang`", `checkoutURL` contract updated, template parse sites get the `t` helper.
- [x] **T416** `english_copy_test.go`: the "checkout Turkish copy must remain" contract is inverted — no Turkish plumbing anywhere in `payment.go`, checkout copy catalog-instrumented; `payment.go` added to the Turkish-copy scan.
- [x] **T417** `views_english_test.go` + `views_test.go`: `TestCheckoutLanguageSwitchingCoversSupportedLocales` (zh-CN/en), critical-state render data migrated to `Locale`/bundle, frozen tokens updated (`value="tr"` → `value="zh-CN"`, `|| 'tr'` → `|| 'zh-CN'`).
- [x] **T418** New buyer tests (`api/handlers/buyer_locale_test.go`): default zh-CN under `Accept-Language: en` (checkout/pay/result), retired inputs ignored, explicit English honored (cookie + legacy cookie), query override, concurrent session isolation, switch links stay on path with both locales, bundle shipped per locale, payment-link error page locale.

## Phase 5 — Verification

- [x] **T419** Focused suites: `go test -count=1 ./localization ./api/middleware ./api/handlers .` in the project image; `go vet` on the touched packages.
- [x] **T420** Catalog coverage gate + sidecar verify + Turkish scan of production sources (templates, handlers, assets).
- [x] **T421** Offline deployment rebuilt from the current tree with `webhook-test.compose.yml`; buyer pages checked over HTTP for `lang`/copy/cookies; postgres, volume, loopback, webhook-test and `GATEWAY_OFFLINE=true` preserved.
- [x] **T422** Browser evidence (browser harness): buyer pages in both locales, language switch refresh persistence, no Turkish copy.
- [x] **T423** Whole-repo suite in the `gotest` image: `go test -p=1 -count=1 ./...` → every package `ok` (399 s). DB-gated tests (repositories, services/database, services/reconciliation, services/txrescan, workers/indexer) skip themselves by design without the `*_TEST_DATABASE_URL` DSNs, so that run is **not** evidence for DB-backed integration paths.
- [x] **T423b** `-race` for `localization` in the same native image (see the verification transcript for the exact command and result).
- [x] **T424 — not applicable.** Changing the `shop.test` notification URL or its language through the portal is not a required action of this localisation work, and the notification URL must not be repointed for it. No merchant-session change is requested from the supervisor. (Evidence: `shop.test` still points at the receiver configured before this task, `updated_at` 2026-10-15 09:44.)
- [x] **T425** Supervisor-found regression fixed (bounded): the payment-link machine language that reaches the edit form is canonical.
  - `api/handlers/dealer.go`: `DealerProductView.Language` is `localization.NormalizeLanguage(stored).HTMLTag()` (legacy `tr` → `zh-CN`), plus a new localized `LanguageLabel` for the row displays; `views/dealer/{dashboard,admin_dashboard}.html` show the label and keep the dataset canonical.
  - `views/assets/dashboard.js`: `canonicalProductLanguage()` mirrors the server rule before assigning the dataset value to the (case-sensitive) select, so an English link can no longer be saved as Chinese.
  - Tests: `api/handlers/dealer_product_language_test.go` (stored `en`/`EN`/`en-US`/`zh-CN`/`ZH_CN`/`zh`/`tr`/`TR`/`fr`/blank → canonical view value + label, rendered `data-product-language`, and the two selects offer exactly `zh-CN,en`), `scripts/test-product-language-edit-select.mjs` (20/20: dataset → select option, including the pre-fix value that used to deselect the select), `api/handlers/fix01c3r_test.go` migrated from the uppercased passthrough to the canonical value.
  - Copy/comment fixes in the same bounded pass: `Expected amount` and the four expected-amount sentences → 应付金额, `Payment received successfully.` → 已收到付款。, `Polling fallback is still checking this payment.` → 备用轮询仍在检查支付状态。, and the `PortalLocale` doc comment now matches the paths the middleware actually handles.

## Open / reported

- `views/gateway/sample_crypto-checkout.html` is **not routed** by any handler (verified by search) and contains English-only demo copy; it stays untouched and is excluded from the buyer surface.
- `alt="Payment QR"`, `Asset` and `Gateway` labels were localized during the audit; `Gateway Pay` remains a brand string.
