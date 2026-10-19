# Feature Specification: English Portal, Admin, and Invoice Surfaces

**Feature Branch**: `002-english-portal`

**Created**: 2026-10-12

**Status**: Accepted for the English-copy scope — implementation and runtime update complete; authenticated dashboard runtime verification remains explicitly deferred in T407.

> Historical migration record: the Turkish→English portal migration described here was superseded by the bilingual (Chinese/English) language contracts in `specs/003-portal-zh-en` and `specs/004-buyer-locale-zh-en`. This document is kept as a record of that migration; it is not the current language contract.

**Input**: "Default the merchant portal, admin panel, and related invoice surfaces to English." The hosted checkout keeps its existing en/tr switching and payment-language defaults; layout, interactions, routes, permissions, and business logic are untouched. No global language switcher and no i18n dependency are introduced.

## Scope

**In scope (user-visible English copy only, UI-display strings):**

| Area | Paths | Turkish-bearing lines (measured) |
| --- | --- | --- |
| Merchant portal shell | `views/dealer/layout.html`, `views/dealer/partials/header.html`, `views/dealer/partials/footer.html`, `views/dealer/home.html` | 1 / 20 / 4 / 1 |
| Merchant portal pages | `views/dealer/dashboard.html`, `login.html`, `register.html`, `onboarding.html`, `oidc_missing.html` | 199 / 9 / 10 / 6 / 5 |
| Admin pages | `views/dealer/admin_dashboard.html`, `admin_login.html`, `admin_2fa_setup.html`, `admin_2fa_verify.html`, `admin_2fa_disable.html` | 200 / 8 / 5 / 8 / 7 |
| Portal/admin JavaScript | `views/assets/dashboard.js` (loaded from `views/dealer/layout.html`) | 45 lines, 51 distinct strings |
| Invoice | `views/invoice.html` | 11 |
| Portal/admin handler copy | `api/handlers/dealer.go` (389 distinct quoted Turkish strings: ~270 admin-scope lines, ~209 merchant-scope, ~30 OIDC), `api/handlers/txrescan.go` (9) | 509 + 15 |
| Document language + display locale | `<html lang="tr">` in `views/invoice.html`, `views/dealer/layout.html`; Turkish collation/casing in `dashboard.js` (`localeCompare(…, 'tr')`, `toLocaleLowerCase('tr-TR')`) | — |

**Explicitly out of scope (must remain byte-identical in behaviour):**

- `views/gateway/checkout.html`, `pay.html`, `payment_result.html`, `sample_crypto-checkout.html` and the checkout/payment strings inside `api/handlers/payment.go` (`TitleTR`/`BodyTR` and the frozen legacy status-label tokens) — the existing `Lang`/`IsEnglish` switching stays as is.
- Payment/product language defaults: `data-product-language || 'tr'` in `dashboard.js` and `Product.Language` handling in `dealer.go` are behavioural fields, not display copy.
- API contract, routes, JSON keys, form `name`/`id`, role/status enums, DB values, asset/chain identifiers, SQL, DOM selectors/`data-*` hooks, API scope values, machine error codes, logs.

## Requirements *(mandatory)*

- **FR-001**: Every in-scope template/JS/handler string that is user-visible copy MUST render English by default, with no language switcher and no new dependency.
- **FR-002**: Protocol/behavioural tokens MUST NOT change: routes, JSON keys, form field names/ids, `data-*` selectors, role/permission/status values, DB values, asset/chain symbols, SQL, machine error codes, log lines.
- **FR-003**: `<html lang>` MUST become `en` on the in-scope surfaces; the checkout templates keep their dynamic `{{.Lang}}` / `IsEnglish` values.
- **FR-004**: Turkish-aware display collation/casing in `dashboard.js` MUST become English-locale behaviour (`localeCompare(…, 'en')`, `toLocaleLowerCase('en-US')`), affecting only visible ordering/filtering.
  **Exception (LANG-03 correction)**: `normalizeAssetKey()` (used at `dashboard.js` lines 958/976/1565/1590 to match `data-asset-key` / `data-balance-key` values against ledger and balance rows) is a **lookup key, not display text**, so it intentionally keeps `toLocaleLowerCase('tr-TR')`. Only the display helper `normalize()` and the admin-table `localeCompare` switch to English; amount parsing and balance/ledger matching are untouched. `views_english_test.go` pins this split.
- **FR-005**: No layout, interaction, route, permission, or business-logic change; no DB writes from this work.
- **FR-006**: Existing Turkish-asserting tests MUST be updated to the new English copy (they assert display text, not protocol), and the suite MUST stay green.
- **FR-007**: `.fiber.br` sidecars MUST be regenerated for any changed asset that has one, using `scripts/brotli-sidecars`.
- **FR-008**: The running offline stack MUST be updated from cache-cached Docker layers (no native Wallet Core rebuild), keeping the DB volume, `.env`, and internal-only network untouched.

## Terminology (recommended English glossary — for supervisor review)

| Legacy term (Chinese meaning) | Recommended English | Notes |
| --- | --- | --- |
| 商户 / 会员商户 | merchant | Portal-wide term; keep "merchant" consistently, never "member business" |
| 域名 | domain | Keep the stored `domain_url` untouched |
| 支付 | payment | |
| 支付请求 / 支付链接 | payment request / payment link | Prefer "payment request" for API-facing lists; "payment link" only where the UI already means the hosted link |
| 发票 | invoice | `/invoice/:token` |
| 提现 / 取款 | withdrawal | Keep enum values (`withdrawal*`) untouched |
| 退款 | refund | |
| 归集 / 清扫 | sweep | Keep `sweep_jobs`, `sweep` prefixes untouched |
| 余额 | balance | |
| 交易 | transaction | Admin list header; avoid "operation" |
| 网络 / 链 | network / chain | Prefer "network" in UI, "chain" where the stored `ChainName` is shown |
| 钱包 | wallet | |
| 地址 | address | |
| 通知 | notification | |
| Webhook（不变） | webhook | Unchanged |
| API 密钥 / 密钥 | API key / API secret | Never expose secret values |
| 设置 | settings | |
| 登录 / 退出 | sign in / sign out | Avoid "login"/"logout" in button labels |
| 注册 / 创建账户 | create account / sign up | Register page title: "Create your merchant account" |
| 保存 / 取消 / 删除 / 编辑 / 搜索 / 全部 | Save / Cancel / Delete / Edit / Search / All | Table/action verbs |
| 状态 | status | |
| 金额 | amount | |
| 日期 / 创建时间 | date / created | |
| 待处理 / 已完成 / 失败 / 已过期 | pending / completed / failed / expired | Mirrors existing status enums (values unchanged) |
| 管理员 | admin | |
| 验证 | verification | 2FA pages |
| 手工测试 | manual test | Admin tooling labels |

Month/day names and number/date formats: use `en-US` display formatting; do not change stored timestamps.

## Success Criteria *(mandatory)*

- **SC-001**: Zero Turkish-diacritic user-visible copy remains in the in-scope paths (automated scan over the enumerated files, excluding checkout templates, protocol tokens, and test fixtures).
- **SC-002**: Rendered pages are English in the running container for: `/`, `/merchant/login`, `/merchant/register`, `/merchant/dashboard`, `/admin/login`, plus one admin page (e.g. `/admin/security`) — verified by HTTP fetch of the served HTML.
- **SC-003**: `views/gateway/*` templates and `payment.go` checkout strings are byte-identical to the LANG-01 baseline (`key-files.sha256`).
- **SC-004**: `go build ./...`, `go vet ./...`, `go test -p=1 -count=1 ./...` exit 0 in the test image.
- **SC-005**: `scripts/brotli-sidecars --verify` reports mismatches=0 after asset changes.
- **SC-006**: `dashboard.js` still parses (`node --check`) and its sort/filter behaviour is English-locale.

## Assumptions

- Test accounts use the existing development `.env` credentials (never printed) and the already-provisioned dev merchant; no new users or DB rows are created by the translation work.
- Checkout's `Lang`/`IsEnglish` plumbing in `payment.go` stays untouched, so the checkout keeps its current en/tr behaviour and default.
- The offline stack is the verification target; the internal-only network, volume, and `.env` are preserved.
