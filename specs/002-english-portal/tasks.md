---

description: "Task breakdown and execution status for the English portal/admin/invoice surfaces"
---

# Tasks: English Portal, Admin, and Invoice Surfaces

**Input**: `specs/002-english-portal/{spec.md,plan.md}` · **Baseline**: `/tmp/opencode/gateway-lang01/`

Legend: `[x]` done with recorded evidence · `[ ]` not done / reserved for the supervisor.

## Phase 0: Reconnaissance and freeze (LANG-01)

- [x] **T001** Baseline manifest: `git status`, `git diff` (tracked), untracked list, `HEAD`/branch, 71 key-file SHA-256 hashes → `/tmp/opencode/gateway-lang01/{baseline.txt,baseline-tracked.diff,untracked.txt,key-files.sha256}` (no `.env` in evidence; verified by scan)
- [x] **T002** Turkish copy census: 20 `views/` files (640 lines), `dashboard.js` 45 lines / 51 strings, `dealer.go` 509 lines / 389 distinct strings, `txrescan.go` 9, `payment.go` 24 (checkout — excluded)
- [x] **T003** Scope freeze and excluded surfaces (checkout templates + payment strings + product/payment language defaults + all protocol tokens)
- [x] **T004** Test-token dependency map: `dealer_test.go` (52), `txrescan_test.go` (9), `payment_test.go` (checkout + invoice assertions), `views_test.go` (fixtures only)
- [x] **T005** Spec Kit artifacts for `specs/002-english-portal/` — no re-init, no branch change
- [x] **T006** Draft English glossary for supervisor review (spec.md §Terminology)

## Phase 1–3: Implementation (LANG-02)

- [x] **T101** Shell + partials + merchant pages: `layout.html` (`lang="en"`), `home.html`, `login.html` (heading now "Sign in securely to manage payments."), `register.html` ("Create your merchant account"), `onboarding.html`, `oidc_missing.html`, `partials/header.html`, `partials/footer.html`
- [x] **T102** Merchant dashboard `views/dealer/dashboard.html` (300 lines, 306 strings incl. display-copy attributes; product-language `value="tr"`/`"en"` and the `tr` default preserved)
- [x] **T103** Admin pages: `admin_dashboard.html` (355 lines), `admin_login.html`, `admin_2fa_setup/verify/disable.html`
- [x] **T104** `views/assets/dashboard.js`: 66 user-visible strings; display locale switched for `normalize()` (`en-US`) and admin sorting (`localeCompare …, 'en'`); **`normalizeAssetKey` deliberately kept `tr-TR`** (ledger/balance lookup key) with a comment and a pinned test
- [x] **T105** `views/invoice.html` (19 strings, `lang="en"`) + `api/handlers/dealer.go` (565 lines / 442 distinct literals) + `api/handlers/txrescan.go` (19)
- [x] **T106** `api/handlers/payment.go` invoice-only hunk (10 lines: invoice title, invoice 404 copy, `invoiceStatusLabel`); checkout copy byte-identical
- [x] **T107** Copy polish: "Sign in securely to manage payments.", "Select a valid checkout session to mark as failed.", "Checkout could not be marked as failed: …"
- [x] **T108** Test sync: `dealer_test.go` 59 assertions, `txrescan_test.go` 9, `payment_test.go` 4 invoice assertions (fixed when the final run surfaced them) — nothing deleted, skipped, or loosened
- [x] **T109** New tests: `views_english_test.go` (29-case render matrix over merchant panels and admin panels incl. security/withdrawals/refunds/sweeps, 2FA, invoice; DOM-hook freeze; checkout en/tr regression; asset-key technical-locale pin) and `api/handlers/english_copy_test.go` (handler copy invariants + invoice/checkout scope pin)
- [x] **T110** Assets: `views/assets/dashboard.js.fiber.br` regenerated (quality 11); `scripts/brotli-sidecars --verify` → 29 sidecars, mismatches=0; `node --check` exit 0

## Phase 4: Verification (LANG-03)

- [x] **T401** Structural freeze (`verify-lang02.py`): Go/JS skeletons (literals + comments stripped) identical for `dealer.go`, `payment.go`, `txrescan.go`, `dashboard.js`; template `{{...}}` action multisets identical; only approved display-copy attribute values and `lang` changed → `RESULT: STRUCTURE OK`
- [x] **T402** Protocol freeze: route literals, API scope values and JSON/map keys unchanged (`dealer.go` 102→102 keys, `txrescan.go` 11→11, `payment.go` 23→23; zero route/scope additions or removals)
- [x] **T403** Checkout freeze: `git diff --name-only HEAD -- views/gateway/ views/assets/checkout.js` empty; `payment.go` hunks confined to lines 2206/2225/2265-2279 (invoice branch), text outside byte-identical
- [x] **T404** Image rebuild: runtime and `gotest` images rebuilt; **all `walletcore` steps CACHED, zero native compile lines**; Go recompiled as required; final images `gateway-offline:local` (runtime) and `gateway-offline:test` (see §LANG-03 evidence)
- [x] **T405** Test battery in the single final `gotest` image (`sha256:bd7acbcd18cf757b81908083770eeddd248bde8772aedcca22ed0a6b214a5034`, rebuilt in 67 s with all `walletcore` steps CACHED): `go build ./...` exit 0, `go vet ./...` exit 0, `go test -p=1 -count=1 ./...` **exit 0 — 43 packages ok, 8 without test files, 0 failures** (254 s) with the four TEST DSNs against the isolated `gateway_test` database (`core`, `core/api/handlers`, `core/repositories`, `core/services/database` all ok). Earlier runs surfaced two stale invoice assertions in `api/handlers/payment_test.go`, fixed in T108 — nothing skipped or weakened.
- [x] **T406** Runtime HTTP verification on `127.0.0.1:3001`: `/`, `/merchant/register`, `/merchant/login`, `/admin/login` → 200, `lang="en"`, zero Turkish diacritic lines, English anchors present; `/merchant/dashboard`, `/merchant/dashboard/products`, `/admin`, `/admin/security` → 303 redirect to the relevant login (no data leaked)
- [ ] **T407** Authenticated **page rendering** (merchant dashboard, admin pages): **not verified at runtime** — corrected in LANG-04. The portal-JWT gate is *not* the blocker: `api/middleware/portal_jwt.go:36-40` issues the `gateway_portal_jwt` cookie on any safe GET, and `views/assets/dashboard.js` (`initPortalJWTProtection()` → `attachPortalJWTInputs()`, lines 1027-1063, called from `DOMContentLoaded`/`initMerchantWorkspace` at lines 5-16) injects `_portal_jwt` into POST forms and patches `fetch`. Running exactly that flow against `127.0.0.1:3001` succeeds: `GET /admin/login` → 200 + cookie (value not printed), `POST /admin/login` with the cookie token verbatim as `_portal_jwt` plus the existing `.env` admin credentials → **303 `Location: /admin/2fa/setup`** (credential login works; the redirect was deliberately not followed because that GET calls `SaveTOTPSecret`). What remains unverified is the rendering of authenticated pages, because (a) admin access additionally requires enrolling 2FA, which is out of scope for this task, and (b) no merchant credential exists (the provisioned development merchant has no password and creating accounts is forbidden). The Go render matrix (`views_english_test.go`) is substitute evidence for **copy**, not an authenticated runtime pass. Browser check performed with the harness's own Chromium (`browser-harness` CLI is not installed): `/merchant/login` renders `lang="en"`, H1 "Sign in securely to manage payments.", zero Turkish diacritics, and the `_portal_jwt` hidden input is JS-injected with a non-empty value (value never printed).
- [x] **T408** Database read-only check: `merchants`/`domains` row counts and content fingerprints identical before/after the stack update (`24673f4f…`, `5cf9b631…`); `transactions`, `ledger_entries`, `payment_sessions`, `sweep_jobs`, `outbound_transactions`, `withdrawal_requests`, `refunds` all 0; 1 admin row with TOTP disabled (`totp_secret_set=0`); only normal login-flow activity occurred (no data written by LANG work)
- [x] **T409** Evidence bundle under `/tmp/opencode/gateway-lang01/evidence/`: build logs, cache proof, test logs, HTTP verification output, protocol-literal freeze, artifacts/hashes, DB snapshots — secret-free

## Phase 5: Supervisor review (reserved)

- [x] **T501** Supervisor reviewed copy and key diffs against the glossary; security warnings retain their meaning, asset-key normalization retains its original technical locale, and only approved display attributes and invoice-specific strings changed.
- [x] **T502** Supervisor acceptance (2026-10-12), limited to English copy: independently fetched the four public pages (200, `lang="en"`, English anchors), matched 25 key source/asset files to the passing test image, and checked ordered template actions and DOM/form structure across 15 changed templates (confirmation text only). Reviewed the final 43-package passing test log, cached native build evidence, invoice-only diffs, and corrected legitimate login flow. T407 remains unverified at runtime; the 29-case render matrix is substitute copy evidence, not proof of authenticated dashboard operation. No credentials, 2FA settings, business data, or permissions were changed; no commit, push, or merge performed.
