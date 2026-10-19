# Feature Specification: Buyer-Flow Locale Unification (zh-CN default, English retained, Turkish removed)

**Feature Branch**: `004-buyer-locale-zh-en`

**Created**: 2026-10-15

**Status**: Draft — implementation in progress (see `tasks.md`)

**Supersedes / amends**: `002-english-portal` (whose English-default rule for the hosted checkout is withdrawn) and extends `003-portal-zh-en` (whose zh-CN default and `localization` mechanism stopped at the portal edge and explicitly froze the buyer flow).

**Input**: "All user-visible content must be zh-CN or English, with Simplified Chinese as the default. A user who jumped from the merchant portal to the hosted buyer payment page still saw a language other than Chinese or English (Turkish). Fix it for real, not by explanation."

## Context

Two independent language systems exist today:

| Surface | Locale source | Supported values | Default |
| --- | --- | --- | --- |
| Portal home, merchant/admin portal, invoice | `localization` package: `?lang=` → cookie `gateway_portal_lang` | `zh-CN`, `en` | `zh-CN` |
| Hosted checkout, pay page, payment result, payment-link landing | `normalizeLanguage()` (`api/handlers/dealer.go`), `checkoutLanguage()` (`api/handlers/payment.go`): `?lang=` → cookie `gateway_lang` | `en`, `tr` | **`tr`** |

The second system is the defect the user hit: the full buyer journey (payment link → checkout → pay → return page → invoice) renders **Turkish** whenever the request carries no recognized value, and `?lang=tr` is a first-class choice. The buyer pages also carry their own fallback-free JS copy (`views/assets/checkout.js` decides Turkish vs English from `document.documentElement.lang`), and `Product.Language` persists `tr` as its column default.

This feature removes the second system: the buyer flow uses the same `localization` mechanism, the same two locales and the same zh-CN default as the portal, with one cookie and one priority order.

## Decisions

| # | Decision |
| --- | --- |
| B1 | **Locales**: exactly `zh-CN` and `en` on every user-visible surface, including the public buyer flow. Simplified Chinese is the default whenever no *recognized explicit* choice exists. |
| B2 | **Turkish is removed as a language, not hidden**: no `tr` option, alias, URL, cookie value, or copy remains. A `tr` value arriving from a stale link, a stored product row or a legacy cookie is *unrecognized* and falls back to the documented default — it is never displayed, and the stored business data is never rewritten. |
| B3 | **One cookie**: `gateway_portal_lang` (`localization.CookieName`) is the only cookie written for language. The old checkout cookie `gateway_lang` is read-only legacy input (recognized `en`/`zh` values only) and stops being written. |
| B4 | **No layout change**: the existing language control on buyer pages (trigger + dialog markup and CSS classes) is kept; only its languages, labels and URLs change. No new dependency, no CSS/font change. |
| B5 | **`Accept-Language` stays ignored** on every surface: the locale is explicit (query > cookie > default), which keeps tests and SSR deterministic. A browser that advertises English must still receive Chinese by default. |
| B6 | **Product/payment-link language narrows to the supported set**: blank, legacy `tr` or any unsupported value resolves to the zh-CN default at display time. No bulk migration of existing rows; the column keeps storing a language string, its default becomes `zh-CN`. |
| B7 | **Machine surfaces are untouched**: routes and path strings, JSON keys, form `name`/`id`, `data-*` hooks, status/event enums, DB values, asset/chain symbols, amounts, addresses, webhook payloads, logs and HTTP statuses stay exactly as they are. |
| B8 | **Server-rendered first paint wins**: the request locale is decided server-side and shipped in the JSON i18n bundle; client scripts must read that bundle, never sniff `document.documentElement.lang` or `navigator.language`, so dynamic updates can never flash back to another language. |
| B9 | **The switch records the choice through the existing middleware** (`?lang=` → cookie + `lang` stripped), so a switched locale survives refresh, in-page navigation and the whole payment-link → checkout → pay → result → invoice journey. |

## User Scenarios & Testing

### User Story 1 — A buyer with no stored preference sees Chinese (Priority: P1)

A payer opens a payment link in a fresh browser (no language cookie) whose browser advertises English.

**Why this priority**: this is the reported defect; it is the single highest-value fix.

**Independent Test**: request the payment-link landing, checkout, pay, payment result and invoice in a cookie-less client with `Accept-Language: en-US` and assert `<html lang="zh-CN">` plus Chinese copy on all five.

**Acceptance Scenarios**

1. **Given** a fresh client with no cookie and no `lang`, **When** the payment link / checkout / pay / result / invoice is requested with `Accept-Language: en-US`, **Then** every page renders `lang="zh-CN"` and Chinese copy (evidence A).
2. **Given** a stored payment link whose `language` is `tr` (legacy row), **When** that link is opened by a cookie-less client, **Then** the checkout renders Chinese and the Turkish string is never rendered (FR-003/FR-008).
3. **Given** a legacy browser holding `gateway_lang=tr`, **When** any buyer page is requested, **Then** the page renders Chinese (the legacy value is unrecognized).

### User Story 2 — An explicit English choice sticks end to end (Priority: P1)

A payer explicitly selects English on the checkout, then continues to the pay page, the payment result, the invoice and back to the checkout.

**Why this priority**: the Chinese default must not break the working English path (regression risk of this change).

**Independent Test**: from a cookie-less client, request `?lang=en`, follow the recorded choice through five buyer pages, and assert English copy plus `lang="en"` on each — including after a refresh and after the `lang` parameter has been stripped from the URL.

**Acceptance Scenarios**

1. **Given** a cookie-less client, **When** `?lang=en` is requested on a buyer page, **Then** the page redirects to the same path without `lang`, stores `gateway_portal_lang=en`, and renders English (evidence B).
2. **Given** the recorded choice, **When** the buyer navigates checkout → pay → result → invoice → checkout without any `lang` parameter, **Then** all pages stay English (evidence B).
3. **Given** an English choice and a concurrent Chinese session in another browser, **When** both request the same pages, **Then** each keeps its own locale (evidence D).

### User Story 3 — No Turkish (or other third language) is reachable (Priority: P1)

**Why this priority**: the user's complaint is the presence of a third language; leaving any reachable path is a failed fix.

**Independent Test**: render every buyer page in both supported locales and scan the rendered output and the shipped JS/CSS for Turkish words (accented and ASCII forms), for `hreflang="tr"`, and for a `tr` language option.

**Acceptance Scenarios**

1. **Given** any buyer page rendered in either locale, **When** the HTML is scanned for Turkish copy, **Then** no Turkish string, `lang="tr"`/`hreflang="tr"` attribute or `tr` language option is present (evidence C).
2. **Given** `?lang=tr` (or `tr-TR`) on any buyer path, **When** the page is rendered, **Then** the value is treated as unrecognized: no cookie is written from it, no redirect strips it as a known choice, and the page renders the default Chinese (evidence C).
3. **Given** the repository sources, **When** scanned for user-visible Turkish copy, **Then** none remains in templates, handler display copy or browser assets (evidence C).

### User Story 4 — Merchant-facing consistency (Priority: P2)

A merchant creates a payment link and chooses its language.

**Why this priority**: the merchant's choice feeds the buyer flow; a stale `tr` option there would re-introduce the defect.

**Independent Test**: render the merchant product/payment-link form and assert the language select offers exactly `zh-CN` and `en` with localized labels, that a saved choice round-trips, and that a blank/legacy value resolves to `zh-CN`.

**Acceptance Scenarios**

1. **Given** the merchant product form, **When** it renders, **Then** the language select contains exactly the two supported values with localized labels (no `tr`).
2. **Given** a product saved with `language=tr`, **When** the checkout is opened through its payment link, **Then** the checkout renders the default Chinese.
3. **Given** a product saved with `language=en`, **When** its payment link is opened, **Then** the checkout renders English and records the choice in the canonical cookie.

### User Story 5 — Behaviour and safety are unchanged (Priority: P1)

**Why this priority**: a locale change must not alter money, auth or protocol behaviour.

**Independent Test**: run the affected Go packages and the existing locale/XSS/concurrency suites; assert payment status transitions, idempotency, webhook payloads and route wiring are byte-identical to the baseline.

**Acceptance Scenarios**

1. **Given** the change, **When** the payment/auth/webhook/route suites run, **Then** they pass without any behaviour-related assertion being modified for locale reasons (evidence E).
2. **Given** hostile `lang` values (script payloads, CRLF, traversal, foreign hosts), **When** they reach the buyer middleware or handlers, **Then** the existing safety guarantees hold: no cookie, no off-origin redirect, no raw input in rendered output (evidence D).
3. **Given** a changed browser asset, **When** the sidecar gate runs, **Then** every `.fiber.br` sidecar decompresses byte-for-byte to its source (evidence F).

### Edge Cases

- `lang` present but empty or whitespace only → treated as absent, no cookie, no redirect.
- `lang` recognized **and** an existing different cookie → the query wins for that response and is recorded (the request that carries it is redirected once, `lang` stripped).
- `lang=zh`, `zh-CN`, `ZH_CN`, `zh-Hans` → `zh-CN`; `en`, `en-US`, `en-GB` → `en`; anything else (including `tr`, `tr-TR`, `de`, `fr`, `zh-Hant`, `en-garbage`, script-shaped input) → unrecognized → default.
- A POST (mutation) request never triggers the locale middleware; it must still render/redirect in the locale already recorded by the cookie.
- Both `gateway_portal_lang` and legacy `gateway_lang` present with different recognized values → the canonical cookie wins; the legacy one is ignored.
- Payment session/status pages that are reached without any prior page (deep link to a result URL) → default Chinese.
- A page that renders dynamic copy after a WebSocket/polling update → uses the same locale as the first paint (no flash to another language).
- Invoice reached from the checkout in the other locale → the invoice follows the recorded cookie, consistent with the rest of the journey.

## Requirements

### Functional Requirements

- **FR-001** Every user-visible surface (portal home, merchant portal, admin panel, invoice, payment-link landing, hosted checkout, pay page, payment result/status page and the sample checkout page) MUST render exactly one of `zh-CN` or `en`; no other language may be displayed.
- **FR-002** When no recognized explicit choice exists, the rendered locale MUST be `zh-CN`, regardless of the browser's `Accept-Language` header.
- **FR-003** The buyer flow MUST resolve its locale with this priority: recognized `lang` query → recognized `gateway_portal_lang` cookie → recognized legacy `gateway_lang` cookie → `zh-CN`. Unrecognized values MUST be ignored at every step.
- **FR-004** `tr` MUST NOT be a supported alias, a switch target, a selectable option, a stored-language default, or rendered copy anywhere.
- **FR-005** The buyer flow MUST NOT write a language cookie other than `gateway_portal_lang`; the legacy `gateway_lang` cookie MUST only be read.
- **FR-006** Recognized `lang` values on buyer GET/HEAD paths MUST be recorded in the canonical cookie and the parameter removed from the URL, preserving all other query parameters and staying same-origin (existing middleware behaviour extended to the buyer paths).
- **FR-007** All static buyer copy MUST come from the zh-CN catalog keyed by the English source text, rendered server-side through the template helper; the English rendering remains the source text itself.
- **FR-008** All browser-side buyer copy MUST be read from the server-rendered JSON bundle; no client code may derive copy from `document.documentElement.lang`, `navigator.language` or a hard-coded language table.
- **FR-009** All handler-produced buyer copy (payment status titles/bodies, result status labels, receipt labels, payer-facing error messages, invoice title/status) MUST be localized per request locale through the catalog.
- **FR-010** The language control on buyer pages MUST keep its existing markup/CSS classes and MUST offer the two supported locales, with localized `title`/`aria-label` text and correct `lang`/`hreflang` attributes.
- **FR-011** `Product.Language` and the payment-session product snapshot MUST accept only the supported set: write-time normalization maps blank/legacy/unsupported values to `zh-CN`; no bulk rewrite of existing rows is performed, and display-time resolution follows FR-003.
- **FR-012** Machine values MUST NOT change: route paths and `CommandType` strings, JSON keys, form field names, `data-*` hooks, status/event enums, DB values, asset and chain symbols, amounts, addresses, hashes, webhook payloads, log lines and HTTP statuses.
- **FR-013** User-generated and third-party content (merchant/product names, descriptions, domains, URLs, activity text) MUST NOT be translated or altered.
- **FR-014** The catalog MUST cover every key requested by templates, handlers and browser assets; a missing key MUST fail the coverage gate rather than silently render English on a Chinese page.
- **FR-015** No third-party i18n dependency may be added; the in-repo `localization` package remains the single mechanism.
- **FR-016** Locale resolution MUST stay a pure function of its arguments: no package-level mutable locale state, no cross-request bleed.
- **FR-017** Every changed browser asset under `views/assets` and `static` MUST have its `.fiber.br` sidecar regenerated.
- **FR-018** Existing safety guarantees (same-origin redirect, escaping of hostile locale/URL values, per-request isolation) MUST hold for the buyer flow after the change.
- **FR-019** Documentation describing the locale mechanism MUST be updated inside the locale scope only.

### Key Entities

- **Locale**: canonical `zh-CN` | `en`; only these two may reach rendering.
- **Language choice**: the recorded user decision, held in the `gateway_portal_lang` cookie; legacy `gateway_lang` is a read-only hint.
- **Product / payment-link language**: stored merchant choice feeding the buyer flow; unsupported values resolve to the default.
- **i18n bundle**: the per-request JSON payload (`portal-i18n-data`) carrying locale + browser strings; the only source of client-side copy.

## Success Criteria

- **SC-001** Cookie-less client with `Accept-Language: en-us` renders `zh-CN` on all five buyer pages plus the portal home (evidence A).
- **SC-002** An explicit `en` choice survives the payment-link → checkout → pay → result → invoice → checkout journey, including refresh, with the `lang` parameter stripped (evidence B).
- **SC-003** Zero Turkish strings, `tr` language options or `tr` attributes in rendered buyer HTML in both locales, in the shipped browser assets, and in repository display copy (evidence C).
- **SC-004** Two concurrent clients with different recorded locales each keep their own locale across repeated renders, and the hostile-input suite still passes (evidence D).
- **SC-005** Payment, authentication, idempotency, webhook and routing test suites pass unchanged except for the documented locale-contract migrations (evidence E).
- **SC-006** Sidecar freshness gate passes for every changed asset (evidence F).

## Assumptions

- The buyer flow is unauthenticated; the locale is therefore per-browser, not per-account.
- Turkish-speaking users are not a supported audience for this deployment; removing the language is intended, not a regression.
- Historical rows and cookies holding `tr` are acceptable to lose as a *preference* — they resolve to Chinese; no data migration is required or wanted.
- The existing CSS for the buyer language control is reusable for two locales without a design change.
