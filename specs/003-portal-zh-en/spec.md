# Feature Specification: Simplified-Chinese Portal/Admin/Invoice Localization (zh-CN default, English retained)

**Feature Branch**: `003-portal-zh-en`

**Created**: 2026-10-12

**Status**: Decisions A1–A7 confirmed by the supervisor; `localization/` core (ZH-02, accepted after the supervisor's independent `-race`/`vet` run) and the full wiring of the portal shell, merchant dashboard, admin panel and invoice (ZH-03) are implemented — host-verified with type checks, the `-race` localization suite, a catalog-coverage gate (instrumented keys only), template/render harnesses, JS checks and sidecar verification. **ZH-03 is not yet accepted for final acceptance**, and **003 is not deployed**: the running image is the previous English build, so the zh/en switch is not live. Remaining: un-instrumented display-copy audit + terminology fixes (Reserve wallet → 储备钱包), the zh half of the dual-locale matrix (T501), the CJK-only rule (T502), concurrency/middleware-boundary/XSS tests (T503), and ZH-04's image build + native full suite + runtime/browser acceptance (T505/T506).

**Input**: "Switch the merchant portal, dealer/admin panel, and invoice page to a **zh-CN default** while keeping English fully available and remembering the visitor's choice." The hosted checkout keeps its existing `en`/`tr` switching and defaults, and nothing in its files may change. No i18n dependency is added; a small in-repo localization module provides the strings.

## Decisions (supervisor, ZH-02)

| # | Decision |
| --- | --- |
| A1 | **Confirmed**: portal/admin/invoice default is **zh-CN**; English stays selectable. |
| A2 | **Approved**: the invoice page also gets the switch, implemented strictly per `specs/003-portal-zh-en/ui-guidance.md` (header partial for public/merchant/admin entrances, invoice header area, GET links, no form submission). |
| A3 | Glossary: **Deposit → 入账** (general), **充值 only for explicit customer top-up**; Withdrawal 提现, Refund 退款, Sweep 归集, Payment link 支付链接, Invoice 发票, **API key API 密钥**, **API secret API 签名密钥**, **Webhook signing secret Webhook 签名密钥** (three distinct strings, never interchangeable). |
| A4 | No CSS/font change. Narrow-screen defects, if any, go back to the designer instead of being fixed ad hoc. |
| A5 | **Confirmed**: portal and admin (and invoice) share the single browser-wide cookie `gateway_portal_lang`. |
| A6 | **Confirmed**: `Accept-Language` is ignored — the locale is explicit (query > cookie > default) so it is deterministic and testable. |
| A7 | **Corrected**: public `/api/v1` responses and all machine JSON (`code`/`schema`/keys) stay English/exact. Portal-internal AJAX responses whose `error`/`message` is displayed in the UI MAY be localized per request locale, but their original `code` keys and HTTP statuses stay unchanged. An unknown downstream `err.Error()` is preserved verbatim as diagnostic text (never translated). Logs and webhook payloads stay English and are never localized. |

## Context

`002-english-portal` made the portal/admin/invoice copy **hard-coded English**. This feature turns that English text into the *source key* of a per-request locale lookup, adds a **zh-CN** catalog, and exposes a language switch. Locale is resolved **per request** — no global/package state, no language mutation of the DOM beyond what the server rendered.

## Scope

**In scope (user-visible copy only + the switch control):**

| Area | Paths (measured in ZH-01) | Notes |
| --- | --- | --- |
| Portal shell | `views/dealer/layout.html` (`lang` attr line 2, `dashboard.js` line 10), `views/dealer/partials/{header,footer}.html` | switcher lives in the header partial (public pages must switch without login) |
| Merchant pages | `views/dealer/{home,login,register,onboarding,oidc_missing}.html`, `views/dealer/dashboard.html` (≈400+ lines, 200+ literals) | |
| Admin pages | `views/dealer/{admin_dashboard,admin_login,admin_2fa_setup,admin_2fa_verify,admin_2fa_disable}.html` | 12 dealer templates total on disk |
| Portal/admin JS | `views/assets/dashboard.js` (66 user-visible strings; `confirm` 245, rotation 257-291, `Copied` 325, balance copy 1891-94, …) + `.fiber.br` sidecar | |
| Invoice | `views/invoice.html` (490 lines, ≈24 literals, inline script ≈460), `api/handlers/payment.go:2181+` (`InvoicePageData`), `:2199-2258` (`HandlePaymentInvoice`), `invoiceStatusLabel` after `:2263` | invoice-only hunks; checkout copy in the same file is byte-frozen |
| Handler copy | `api/handlers/dealer.go` (442 literals), `api/handlers/txrescan.go` (19) | display copy only |
| New module | `localization/` (locale resolution + catalogs + JS bundle) | no third-party dependency |

**Coverage estimate**: ≈ **1,240 user-visible strings** (templates ≈700, `dealer.go` 442, `txrescan.go` 19, `dashboard.js` 66, invoice 10); one zh-CN translation each; the JS subset (≈66+) is additionally shipped in the per-page bundle.

**Explicitly out of scope (byte-frozen):**

- `views/gateway/{checkout,pay,payment_result,sample_crypto-checkout}.html`, `views/assets/checkout.js`, `views/assets/checkout.js.fiber.br`, the checkout strings and `TitleTR`/`BodyTR`/status labels inside `api/handlers/payment.go`, and `checkoutLanguage()`/`gateway_lang` (`payment.go:1977-1990`). Verified in ZH-01: `git diff HEAD -- views/gateway/ views/assets/checkout.js` is **empty**.
- Protocol and machine surfaces: routes and `CommandType` path strings, JSON keys, form `name`/`id`, `data-*` hooks, role/permission/status enum values, DB values, asset/chain symbols, API scopes, HTTP statuses, machine error codes, log lines and log message language, webhook payloads, `normalizeAssetKey()`'s technical `tr-TR` casing (defined `dashboard.js:2422-27`; call sites 958/976/1565/1590).
- Product/payment-link language semantics (`DealerPageData.Language`, `normalizeLanguage`, `data-product-language`): these are stored behaviour, **not** UI locale.
- User input, stored history, activity/audit rows, transaction hashes, amounts and merchant-owned text: never translated.

**Users / scenarios**

- A Chinese-first merchant opens `/merchant/login` or `/merchant/register` (no session) and sees Chinese; clicking "English" switches the page and the choice sticks for later visits.
- An operator opens `/admin/login` → `/admin/...`; the switch works pre-login and per browser.
- A merchant admin mid-session changes language via the header switch; the current path and its other query parameters survive, and no POST/fund action is triggered.
- A payer opens `/invoice/:token`; the invoice copy follows the portal locale if that browser has one, English otherwise for `?lang=en`.

## Requirements *(mandatory)*

- **FR-001** Portal, dealer/admin, and invoice HTML MUST default to **zh-CN**; English MUST remain selectable and MUST be complete (every key has an English rendering by construction — the English source text IS the key).
- **FR-002** Locale resolution priority, per request: explicit `lang` query (**`zh`/`zh-CN`/`zh-Hans` → zh-CN; `en`/`en-US` → en**) > portal locale cookie `gateway_portal_lang` > default `zh-CN`. Unrecognized values MUST be ignored (no cookie written, no redirect).
- **FR-003** The portal cookie `gateway_portal_lang` MUST be distinct from the checkout cookie `gateway_lang` and MUST NOT be read or written by any checkout code path; the checkout default (`en`/`tr` behaviour) MUST NOT change. The cookie is written **only** on a GET/HEAD request carrying a recognized `lang` query value; attributes: `Path=/`, `HTTPOnly`, `SameSite=Lax`, one-year `MaxAge`, and `Secure` following the actual request scheme (HTTPS → on).
- **FR-004** The switch MUST work on public/auth pages without login, MUST be a plain GET link (`?lang=…` on the current path) placed per `ui-guidance.md`, MUST preserve the current path and every other query parameter, MUST NOT exist inside a form (no accidental submission), MUST NOT run on POST, and MUST NOT trigger payments, withdrawals, sweeps, refunds or any state-changing work. The switch answers with **303 See Other** (approved implementation decision).
- **FR-005** Locale MUST be resolved per request into the page data; the application MUST NOT keep package-level mutable language state, and MUST NOT mutate `document.documentElement.lang` or any other global JS language state (the server-rendered `lang` attribute is authoritative).
- **FR-006** No rendered-HTML string replacement, no reflection-based translation of struct fields, no automatic translation of user input, stored records, or DOM machine values (`data-*`, form values, `href`s).
- **FR-007** Template translation MUST receive an **explicit locale argument** (`{{ t .Locale "…" }}`); the localization package MUST return plain `string` (never `template.HTML`); Go format strings MUST keep their verbs (`%s`/`%d`/`%q`) and their per-locale order, and callers MUST keep passing the same arguments.
- **FR-008** The JS string bundle MUST come from the trusted catalog (never user data) and MUST be delivered as a **non-executing** `<script type="application/json" id="portal-i18n-data">` element whose content is `json.Marshal` output (HTML-escaped `<`, `>`, `&` → `\u003c…`) passed through a narrow `template.JS` boundary — never `template.HTML`. `dashboard.js` (and the invoice's standalone script) MUST parse the element once with `JSON.parse` into a document-local value, MUST NOT set `document.documentElement.lang`, and MUST NOT keep a cross-request global language state. The same mechanism applies to the standalone invoice page.
- **FR-009** JS display-locale changes are limited to visible sorting/filtering (`localeCompare` `dashboard.js:2298`, `normalize()` `:2548`); `normalizeAssetKey()` and every asset/balance lookup MUST keep `tr-TR` semantics; asset keys, amounts, and balance matching MUST behave identically in both languages.
- **FR-010** Flash/validation messages MUST be generated with the locale of the request that produced them (one-shot flash cookie unchanged); they are NOT retranslated when later displayed.
- **FR-011** `<html lang>` MUST be `zh-CN` or `en` matching the resolved locale on in-scope pages; checkout templates keep their dynamic `{{.Lang}}` behaviour.
- **FR-012** `.fiber.br` sidecars MUST be regenerated for changed assets; `scripts/brotli-sidecars --verify` MUST report no mismatch.
- **FR-013** Tests MUST cover a **dual-locale render matrix** (zh-CN and en) over the existing **29** cases of `englishRenderCases()` (8 `publicView` + 9 `merchantPanel` + 6 `adminPanel` + 6 direct struct cases — static count; the authoritative number comes from `len(englishRenderCases())` / `go test -v` output), plus the invoice test, while the machine contracts (routes, JSON keys, `data-*` hooks, enum values, protocol literals) stay pinned by the existing freeze tests. No existing case may be deleted.
- **FR-014** No new dependency, no database write, no `.env`/auth/2FA/permission change, no checkout or fund-logic change, no translation of logs, webhook payloads or machine JSON.
- **FR-015** Page-data constructors (`dealerPageData`, `adminLoginPageData`, `adminPageData`, the invoice builder) MUST receive the resolved locale explicitly — they currently take no `*fiber.Ctx`. No package-level or context-global locale may be read inside them.
- **FR-016** In templates, the locale is reached through the root data (`$.Locale`) inside `{{range}}`/nested blocks — never by re-deriving it from a child object.
- **FR-017** Browser formatting of parameterized strings MUST use a callback/replacer form (or an equivalent single-pass implementation) so that every `%s`/`%d` consumes its own argument; arguments containing `$&`, `$1`, `%`, `%s` or HTML must not be re-interpreted or double-substituted.
- **FR-018** Portal-internal AJAX human-facing `error`/`message` text follows A7: localized per request locale, while JSON keys, `code` values and HTTP statuses are untouched; `/api/v1` and public machine responses stay English/exact.

## Terminology (zh-CN glossary — supervisor sign-off)

| English (source key) | 中文 | Note |
| --- | --- | --- |
| merchant | 商户 | portal-wide; never 会员企业 |
| domain | 域名 | stored `domain_url` untouched |
| deposit | 入账 | general wording; **充值** only where the copy explicitly means a customer top-up (A3) |
| withdrawal | 提现 | enum values unchanged |
| refund | 退款 | |
| sweep | 归集 | routes/`sweep_jobs` unchanged |
| balance | 余额 | |
| payment link | 支付链接 | |
| invoice | 发票 | `/invoice/:token` |
| API key / API secret / webhook signing secret | API 密钥 / API 签名密钥 / Webhook 签名密钥 | three distinct strings (A3); never render secret values |
| sign in / sign out | 登录 / 退出登录 | button labels |
| status / amount / date | 状态 / 金额 / 日期 | table headers |
| pending / completed / failed / expired | 待处理 / 已完成 / 失败 / 已过期 | labels only, enum values unchanged |
| settings / wallet / address / network / transaction | 设置 / 钱包 / 地址 / 网络 / 交易 | |
| webhook | Webhook | keep as-is |

## Success criteria

- **SC-001** Visiting any in-scope page with no query and no cookie renders zh-CN (`lang="zh-CN"`, Chinese anchors, zero Turkish diacritics).
- **SC-002** `?lang=en` renders English on the same URL and sets `gateway_portal_lang=en`; the URL after the redirect contains no `lang` but keeps every other parameter; later visits without `lang` stay English.
- **SC-003** `?lang=zh-CN` returns to Chinese and the cookie follows; removing the cookie returns to the zh-CN default.
- **SC-004** Checkout (`/checkout/:token`, `/pay/:token`, result pages) is byte-identical in behaviour for `en`/`tr`, including the `gateway_lang` cookie.
- **SC-005** Concurrent requests with different locales never mix languages (render-level concurrency test + `-race` on the localization package).
- **SC-006** Machine contracts unchanged: protocol/route/JSON-key/enum/`data-*` freeze tests pass; `normalizeAssetKey` still `tr-TR`.
- **SC-007** `go build ./...`, `go vet ./...`, `go test ./...` (43+ packages) pass in the cached-native `gotest` image; sidecar `--verify` clean.
- **SC-008** Public/auth switch verified over HTTP and in a real browser without login; authenticated pages verified as far as the environment allows (2FA/credentials limits reported, never bypassed).

## Assumptions

- The 002 English text stays the canonical source key (approach A; see `plan.md`), with an optional `#context` discriminator for the rare same-text-different-meaning cases.
- Translation quality/terminology is the supervisor's call; the Chinese catalog is data, reviewable line by line.
