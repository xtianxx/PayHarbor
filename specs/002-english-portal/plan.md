# Implementation Plan: English Portal, Admin, and Invoice Surfaces

**Branch**: `002-english-portal` | **Date**: 2026-10-12 | **Spec**: [spec.md](./spec.md)

**Input**: `specs/002-english-portal/spec.md` (LANG-01 reconnaissance)

## Summary

Replace Turkish user-visible copy with English across the merchant portal, admin panel, portal/admin JavaScript, and the invoice page, and switch display language/locale to English (`<html lang="en">`, `en` collation). No i18n layer, no switcher, no dependency, no route/permission/logic change. Checkout templates and payment language defaults stay untouched.

## Technical Context

**Language/Version**: Go 1.25.4; server-rendered `html/template` via Fiber v3's html engine; vanilla ES5-style JS in `views/assets/dashboard.js` (no bundler).

**Primary Dependencies**: none added. Text lives inline in templates, in Go handler structs/messages, and in JS string literals.

**Storage**: unchanged (no schema or data change; nothing in scope writes to the database).

**Testing**: existing Go suites (`go test ./...` in the `gotest` image), `views_test.go` render coverage, `node --check` for the JS file, `scripts/brotli-sidecars --verify`, then HTTP/browser verification against the running offline stack.

**Target Platform**: Linux container (offline stack `gateway-offline`), developer host WSL2 Ubuntu 24.04.

**Constraints**: copy-only change; protocol tokens frozen (routes, JSON keys, form names/ids, `data-*` hooks, enums, DB values, asset/chain symbols, SQL, error codes, logs); checkout files byte-identical; no new users/DB rows; keep `.env`, DB volume, and internal-only network; no native Wallet Core rebuild — reuse the existing **Docker build cache** (only the `views/` copy layer changes in the runtime stage).

**Scale/Scope**: 20 Turkish-bearing files in `views/` (~640 Turkish lines, heaviest: `admin_dashboard.html` 200, `dashboard.html` 199, `dashboard.js` 45/51 strings, `partials/header.html` 20, `invoice.html` 11), plus `api/handlers/dealer.go` (389 distinct strings; ~270 admin-scope, ~209 merchant-scope, ~30 OIDC) and `api/handlers/txrescan.go` (9). Test files with Turkish assertions: `api/handlers/dealer_test.go` (52), `txrescan_test.go` (9), `views_test.go` (2 fixtures, no assertions).

## Constitution Check

| Principle | Status | Evidence |
| --- | --- | --- |
| Copy-only change; no money-path or module-boundary impact (`docs/module-boundaries.md`) | PASS | Only display strings and display locale change; no repository/service/ledger code touched. |
| No new dependencies, no UI structure change | PASS | Text substitution in existing files; no switcher, no i18n package, no dependency. |
| API contract and protocol stability (`docs/integration-guide.md`) | PASS | Routes, JSON keys, field names, scopes, enums, DB values, and machine codes are explicitly frozen; checkout `Lang`/`IsEnglish` plumbing untouched. |
| Observability unchanged | PASS | Log lines and error codes are out of scope. |
| Verification must be real, not assumed | PASS | Acceptance includes HTTP fetch of served HTML from the running container plus the full Go suite. |

## Approach

1. **Freeze first**: hash the in-scope files (already captured: `/tmp/opencode/gateway-lang01/key-files.sha256`), and after the change verify `views/gateway/*` + `payment.go` are byte-identical.
2. **Order of work** (keeps diffs reviewable, one area per commit-sized unit):
   a. shell + partials + `html lang` (`layout.html`, `partials/header.html`, `partials/footer.html`, `home.html`);
   b. merchant pages (`dashboard.html`, `login.html`, `register.html`, `onboarding.html`, `oidc_missing.html`);
   c. admin pages (`admin_dashboard.html`, `admin_login.html`, `admin_2fa_*.html`);
   d. `views/assets/dashboard.js` (strings + English collation) and its sidecar;
   e. `views/invoice.html`;
   f. handler copy (`dealer.go`, `txrescan.go`) — classify each Turkish string as display copy vs machine value before touching it;
   g. update Turkish-asserting tests to the new copy.
3. **Token discipline**: never rename `name=`/`id=`/`data-*`, route strings, enum values, `ChainName`/symbols, or JSON keys; where a Turkish string also feeds a lookup/compare, keep the stored value and change only the label.
4. **Locale discipline**: `localeCompare(…, 'en')`, `toLocaleLowerCase('en-US')` for display sorting/filtering; leave `data-product-language` and product-language defaults alone (checkout behaviour).
5. **Assets**: regenerate `.fiber.br` sidecars for changed files with `.fiber.br` siblings (`dashboard.js`, `invoice.html` has none — verify), then `scripts/brotli-sidecars --verify`.
6. **Stack update without native rebuild**: `docker compose … up -d --build` reuses cached `walletcore`/`gobuild` layers and only rewrites the runtime `COPY views/` layer; the container is recreated, the `gateway-offline-pgdata` volume, `.env`, and internal network are untouched. No migration runs (already applied).

## Risks

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Brittle assertions on Turkish copy | Suite red after the change | Update `dealer_test.go`/`txrescan_test.go` expectations in the same unit of work; run the full suite in the `gotest` image. |
| Missed Turkish strings (no i18n layer, large files) | Inconsistent UI language | Automated diacritic scan over the enumerated paths as a gate; manual cell-by-cell review for `admin_dashboard.html` (2 702 lines) and `dealer.go` (389 strings). |
| A "copy" string that is actually a lookup key or enum (e.g. status labels, sweep reasons, product language) | Behaviour regression | Per-string classification before editing; prefer label-only changes; freeze list in FR-002. |
| `dashboard.js` is shared by portal and admin | One change affects both surfaces | Treat it as one unit with its own scan + `node --check` + browser smoke on both dashboards. |
| Turkish-specific casing affecting filter/compare semantics | Visible ordering/filter change | Keep semantics identical, only the locale argument changes; verify the sort/filter smoke path in the browser. |
| Sidecar drift | `TestFiberBrotliSidecarsMatchSources` fails | Regenerate with the committed tool and run `--verify`. |
| Checkout accidentally touched | Breaks the existing en/tr switch | Baseline hashes for `views/gateway/*` and `payment.go`; SC-003 compares them post-change. |

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
| --- | --- | --- |
| Hard-coded English instead of an i18n layer | Supervisor decision for this task: default language change only, no switcher, no dependency | Introducing an i18n framework would change layout/JS structure and add dependencies, both explicitly out of scope |
| One shared JS file serves two portals | Avoiding duplication and new bundles | Splitting the file would change asset/route wiring and break the "no structural change" constraint |

## Phase Plan

- **Phase 0 (done, LANG-01)**: reconnaissance, baseline manifest/hashes, Spec Kit artifacts, glossary draft, scope freeze.
- **Phase 1**: shell/partials/lang attribute; merchant pages.
- **Phase 2**: admin pages; shared `dashboard.js` (strings + locale).
- **Phase 3**: invoice page; handler copy (`dealer.go`, `txrescan.go`); test-copy updates.
- **Phase 4 (verification)**: diacritic scan, frozen-file hash comparison, `go build`/`vet`/`test`, sidecar verify, cached image rebuild + container recreate, HTTP/browser confirmation on `/`, `/merchant/login`, `/merchant/register`, `/merchant/dashboard`, `/admin/login`, one admin page.
- **Phase 5**: supervisor copy review and independent acceptance.
