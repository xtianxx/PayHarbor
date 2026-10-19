package docs

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestIntegrationGuideMatchesEpic1PartnerContract(t *testing.T) {
	guideBytes, err := os.ReadFile("integration-guide.md")
	if err != nil {
		t.Fatalf("read integration guide: %v", err)
	}
	guide := string(guideBytes)

	requireContains(t, guide, "method + path/query + timestamp + raw body")
	requireContains(t, guide, "POST\\n/api/v1/payment/create")
	requireNotContains(t, guide, "message = timestamp + body")

	paymentSection := requireAnchorSection(t, guide, "create-hosted-payment", "create-static-deposit-address")
	requireContains(t, paymentSection, `"result": "ok"`)
	requireContains(t, paymentSection, `"data": {`)
	requireContains(t, paymentSection, `"track_id"`)
	requireContains(t, paymentSection, `"expected_amount_raw"`)
	requireContains(t, paymentSection, `"deposit_address"`)
	requireContains(t, paymentSection, "Idempotency-Key")
	requireContains(t, paymentSection, "409")
	requireNotContains(t, paymentSection, `"success": true`)

	staticSection := requireAnchorSection(t, guide, "create-static-deposit-address", "create-wallet-provider-wallet")
	for _, token := range []string{`"product_id"`, `"chain_id"`, `"token"`, `"created_at"`, "域/产品/用户/资产"} {
		requireContains(t, staticSection, token)
	}

	statusSection := requireAnchorSection(t, guide, "query-payment", "create-payout-request")
	for _, token := range []string{
		"`active`",
		"`pending`",
		"`confirming`",
		"`paid`",
		"`expired`",
		"`canceled`",
		"`failed`",
		"`underpaid`",
		"`overpaid`",
		"`partial_paid`",
		"`payable`",
		"`terminal`",
		"`payment_outcome`",
		"`matched_amount_raw`",
		"`shortfall_amount_raw`",
		"`excess_amount_raw`",
		"`aggregate_complete`",
		"`partial_aggregating`",
		"`missing_memo`",
		"`wrong_memo`",
		"策略启用的聚合匹配",
	} {
		requireContains(t, statusSection, token)
	}

	errorSection := requireAnchorSection(t, guide, "error-handling", "security-checklist")
	requireContains(t, errorSection, `"result": "error"`)
	requireContains(t, errorSection, `"message"`)
	requireContains(t, errorSection, "PayHarbor 可能以相同 event id/type/version 重放失败或已死信的 Webhook")
	requireNotContains(t, errorSection, `"error":`)

	webhookSection := requireAnchorSection(t, guide, "webhooks", "transaction-webhook-payload")
	for _, token := range []string{
		"Webhook 投递是至少一次",
		"操作员重放可能多次投递同一事件",
		"X-Gateway-Event-Id",
		"event_id",
		"event_type",
		"event_version",
		"merchant_id",
		"domain_id",
		"而不是新的业务事件",
	} {
		requireContains(t, webhookSection, token)
	}

	for _, forbidden := range []string{"gw_secret_", "api_secret_value", "webhook_secret_value", "private_key", "mnemonic", "panic:", "goroutine "} {
		requireNotContains(t, guide, forbidden)
	}
}

func TestEpic1IntegrationEvidenceDocumentsCoveredContract(t *testing.T) {
	evidenceBytes, err := os.ReadFile("epic-1-integration-evidence.md")
	if err != nil {
		t.Fatalf("read Epic 1 integration evidence: %v", err)
	}
	evidence := string(evidenceBytes)
	for _, token := range []string{
		"POST /api/v1/payment/create",
		"POST /api/v1/payment/static-address",
		"GET /checkout/{token}/status.json",
		"X-API-Key",
		"Authorization: Bearer",
		"Idempotency-Key",
		"domain/product/user/asset",
		"terminal",
		"payable",
		"Known Production Limitations",
		"go test -count=1 ./...",
		"go vet ./...",
	} {
		requireContains(t, evidence, token)
	}
}

func TestEpic2IntegrationEvidenceDocumentsEventDeliveryContract(t *testing.T) {
	evidenceBytes, err := os.ReadFile("epic-2-integration-evidence.md")
	if err != nil {
		t.Fatalf("read Epic 2 integration evidence: %v", err)
	}
	evidence := string(evidenceBytes)
	for _, token := range []string{
		"docs/money-event-catalog.md",
		"docs/outbox-migration-plan.md",
		"webhook_deliveries",
		"Replay and dead-letter",
		"stable consumer idempotency metadata",
		"native_transfer",
		"payment_succeeded",
		"payout.*.v1",
		"at-least-once delivery",
		"compatibility snapshot tests",
		"go test -count=1 ./services/webhook ./docs",
		"go vet ./...",
		"does not claim full production custody readiness",
	} {
		requireContains(t, evidence, token)
	}
}

func TestChainFactBoundaryDocumentsIndexerContract(t *testing.T) {
	docBytes, err := os.ReadFile("chain-fact-boundary.md")
	if err != nil {
		t.Fatalf("read chain fact boundary: %v", err)
	}
	doc := string(docBytes)
	for _, token := range []string{
		"Chain indexers produce durable chain facts",
		"do not mark payments paid",
		"post ledger entries",
		"enqueue webhooks",
		"chain_id",
		"log index",
		"observed address",
		"finality metadata",
		"CHAIN_<id>_START_BLOCK",
		"Default safe/latest startup is not historical backfill",
		"Range replay/backfill remains",
		"no-op by event id",
	} {
		requireContains(t, doc, token)
	}
}

func TestModuleBoundariesDocumentRuntimeAndMoneyOwnership(t *testing.T) {
	docBytes, err := os.ReadFile("module-boundaries.md")
	if err != nil {
		t.Fatalf("read module boundaries: %v", err)
	}
	doc := string(docBytes)
	for _, token := range []string{
		"modular monolith",
		"application.CompositionRoot",
		"workers/supervisor",
		"context cancellation",
		"restart policy",
		"shutdown order",
		"Chain indexer",
		"Deposits",
		"Payments",
		"Ledger",
		"Outbound",
		"Webhooks",
		"Reconciliation",
		"Chain listeners record facts only",
		"Worker loops must accept cancellation",
	} {
		requireContains(t, doc, token)
	}
}

func TestDepositFinalityBoundaryDocumentsSettlementGate(t *testing.T) {
	docBytes, err := os.ReadFile("deposit-finality-boundary.md")
	if err != nil {
		t.Fatalf("read deposit finality boundary: %v", err)
	}
	doc := string(docBytes)
	for _, token := range []string{
		"Deposit processing consumes durable chain facts",
		"listener path remains fact-only",
		"chain fact event id as the stable idempotency key",
		"Unknown addresses and unrelated chain activity never enter",
		"`pending` or `confirming`",
		"`deposit.finalized.v1`",
		"Bitcoin: 3 confirmations",
		"Solana: 1 confirmation",
		"TRON: 20 confirmations",
		"EVM/default chains: 12 confirmations",
		"must not mark payments paid",
		"post available ledger entries",
		"schedule sweep jobs",
	} {
		requireContains(t, doc, token)
	}
}

func TestLedgerBalanceAuthorityDocumentsLedgerOnlyContract(t *testing.T) {
	docBytes, err := os.ReadFile("ledger-balance-authority.md")
	if err != nil {
		t.Fatalf("read ledger balance authority: %v", err)
	}
	doc := string(docBytes)
	for _, token := range []string{
		"Ledger entries are the only authoritative source",
		"LedgerRepo.DomainBalances",
		"LedgerRepo.WalletBalances",
		"LedgerRepo.MerchantBalances",
		"LedgerRepo.WalletBalancesByWalletIDs",
		"must not be summed as authoritative balance",
		"`merchant_pending`",
		"`merchant_available`",
		"`withdrawal_transit`",
		"`refund_transit`",
		"`platform_clearing`",
		"`voided` entries are excluded",
		"`reorg_reversal` entries compensate",
		"`adjustment` entries",
		"`ux_ledger_idempotent_account`",
		"LedgerRepo.FindInvariantIssues",
		"`correlation_id`",
		"merchant id",
		"domain id",
		"must not include API secrets",
		"GORM-owned through model tags, `AutoMigrate`, and `services/database.VerifySchema`",
	} {
		requireContains(t, doc, token)
	}
}

func TestMoneyEventCatalogDocumentsVersionedEvents(t *testing.T) {
	catalogBytes, err := os.ReadFile("money-event-catalog.md")
	if err != nil {
		t.Fatalf("read money event catalog: %v", err)
	}
	catalog := string(catalogBytes)
	for _, token := range []string{
		"deposit.detected.v1",
		"deposit.finalized.v1",
		"payment.succeeded.v1",
		"payment.failed.v1",
		"payment.expired.v1",
		"withdrawal.requested.v1",
		"withdrawal.broadcast.v1",
		"withdrawal.finalized.v1",
		"withdrawal.failed.v1",
		"refund.succeeded.v1",
		"sweep.succeeded.v1",
		"transaction.reorged.v1",
		"native_transfer",
		"payment_succeeded",
		"payout.requested.v1",
		"event_id",
		"event_type",
		"event_version",
		"occurred_at",
		"idempotency_key",
		"correlation_id",
		"non-destructive",
		"timestamp + raw_body",
	} {
		requireContains(t, catalog, token)
	}
	for _, forbidden := range []string{"api_secret", "webhook_secret", "private_key", "mnemonic", "raw_signature", "stack_trace"} {
		requireNotContains(t, catalog, forbidden)
	}

	guideBytes, err := os.ReadFile("integration-guide.md")
	if err != nil {
		t.Fatalf("read integration guide: %v", err)
	}
	requireContains(t, string(guideBytes), "docs/money-event-catalog.md")
}

func TestMoneyEventOutboxMigrationPlanDocumentsSchema(t *testing.T) {
	planBytes, err := os.ReadFile("outbox-migration-plan.md")
	if err != nil {
		t.Fatalf("read outbox migration plan: %v", err)
	}
	plan := string(planBytes)
	for _, token := range []string{
		"money_event_outboxes",
		"event_id",
		"event_type",
		"event_version",
		"aggregate_type",
		"aggregate_id",
		"merchant_id",
		"domain_id",
		"idempotency_key",
		"payload_json",
		"status",
		"attempts",
		"created_at",
		"ux_money_event_outboxes_event_id",
		"ux_money_event_outboxes_idempotency_scope",
		"AutoMigrate",
		"ApplyGORMMigrations",
		"GORM",
		"Migrator",
	} {
		requireContains(t, plan, token)
	}
	for _, forbidden := range []string{
		"CREATE TABLE",
		"CREATE UNIQUE INDEX",
		"DROP TABLE",
		"```sql",
	} {
		requireNotContains(t, plan, forbidden)
	}
}

func TestEpic5OperationsDocsDocumentGORMMigrationsObservabilityAndLaunchGates(t *testing.T) {
	migrationBytes, err := os.ReadFile("production-migration-discipline.md")
	if err != nil {
		t.Fatalf("read production migration discipline: %v", err)
	}
	migrationDoc := string(migrationBytes)
	for _, token := range []string{
		"versioned GORM migration artifacts",
		"services/dbmigrations",
		"202606280001_provider_health_wallet_address_lookup",
		"202606280002_wallet_address_lifecycle_pool",
		"202606280003_immutable_ledger_journal_projection",
		"GATEWAY_DB_MIGRATION_VERSION",
		"WalletAddressLookupRepo.BackfillWallets",
		"LedgerRepo.RebuildBalanceProjections",
		"database.ApplyGORMMigrations",
		"database.VerifySchema",
	} {
		requireContains(t, migrationDoc, token)
	}
	for _, forbidden := range []string{"CREATE TABLE", "CREATE INDEX", "DROP TABLE", "```sql"} {
		requireNotContains(t, migrationDoc, forbidden)
	}

	runbookBytes, err := os.ReadFile("money-path-observability-runbook.md")
	if err != nil {
		t.Fatalf("read money path observability runbook: %v", err)
	}
	runbook := string(runbookBytes)
	for _, token := range []string{
		"gateway_money_event_outbox_backlog",
		"gateway_webhook_delivery_backlog",
		"gateway_sweep_job_backlog",
		"gateway_withdrawal_backlog",
		"gateway_refund_backlog",
		"gateway_reconciliation_jobs",
		"gateway_provider_health",
		"gateway_wallet_address_lookup_rows",
		"deploy/prometheus/gateway-alerts.yaml",
		"生产日志通过运维 logger 输出结构化 JSON",
		"trace_id",
		"紧急冻结",
		"备份与恢复演练",
	} {
		requireContains(t, runbook, token)
	}
	for _, forbidden := range []string{"api_secret", "webhook_secret", "private_key", "mnemonic", "raw_signature"} {
		requireNotContains(t, runbook, forbidden)
	}

	dashboardBytes, err := os.ReadFile("money-path-dashboard.md")
	if err != nil {
		t.Fatalf("read money path dashboard: %v", err)
	}
	dashboard := string(dashboardBytes)
	for _, token := range []string{
		"Chain state age",
		"Provider lag",
		"Webhook backlog",
		"Signer readiness",
		"Sweep recovery",
		"Reconciliation drift",
		"Trace Drilldown",
	} {
		requireContains(t, dashboard, token)
	}
	for _, forbidden := range []string{"api_secret", "webhook_secret", "private_key", "mnemonic", "raw_signature"} {
		requireNotContains(t, dashboard, forbidden)
	}

	launchBytes, err := os.ReadFile("controlled-launch-readiness.md")
	if err != nil {
		t.Fatalf("read controlled launch readiness: %v", err)
	}
	launch := string(launchBytes)
	for _, token := range []string{
		"Controlled Merchant/Dealer Beta",
		"Production Payment Gateway",
		"Wallet-Provider Custody",
		"Exchange-Grade Tracking",
		"Unsupported scale claims are explicitly rejected",
		"external signer implementation",
	} {
		requireContains(t, launch, token)
	}
}

func TestSwaggerContainsEpic1PartnerContract(t *testing.T) {
	swaggerBytes, err := os.ReadFile("swagger.json")
	if err != nil {
		t.Fatalf("read swagger: %v", err)
	}
	var swagger map[string]any
	if err := json.Unmarshal(swaggerBytes, &swagger); err != nil {
		t.Fatalf("parse swagger: %v", err)
	}

	paths := asMap(t, swagger["paths"], "paths")
	defs := asMap(t, swagger["definitions"], "definitions")
	securityDefs := asMap(t, swagger["securityDefinitions"], "securityDefinitions")

	if _, ok := securityDefs["ApiKeyAuth"]; !ok {
		t.Fatalf("Swagger securityDefinitions missing ApiKeyAuth")
	}
	if _, ok := securityDefs["BearerAuth"]; !ok {
		t.Fatalf("Swagger securityDefinitions missing BearerAuth")
	}

	paymentCreate := operation(t, paths, "/api/v1/payment/create", "post")
	requireOperationParam(t, paymentCreate, "X-Gateway-Signature")
	requireOperationResponse(t, paymentCreate, "201", "#/definitions/types.V1PaymentCreateResponse")
	requireOperationResponse(t, paymentCreate, "409", "#/definitions/types.V1ErrorResponse")

	staticCreate := operation(t, paths, "/api/v1/payment/static-address", "post")
	requireOperationParam(t, staticCreate, "X-Gateway-Signature")
	requireOperationResponse(t, staticCreate, "200", "#/definitions/types.V1StaticAddressResponse")

	paymentHistory := operation(t, paths, "/api/v1/payment/history", "get")
	requireOperationParamEnum(t, paymentHistory, "status", []string{
		"pending",
		"awaiting_payment",
		"paid",
		"expired",
		"canceled",
		"failed",
		"underpaid",
		"overpaid",
		"partial_paid",
	})

	checkoutStatus := operation(t, paths, "/checkout/{token}/status.json", "get")
	requireOperationResponse(t, checkoutStatus, "200", "#/definitions/types.PaymentStatusResponse")

	paymentData := definitionProperties(t, defs, "types.V1PaymentCreatedData")
	for _, field := range []string{"payment_id", "track_id", "session_token", "checkout_url", "status", "expires_at", "order_id", "amount", "currency", "chain_id", "symbol", "token", "decimals", "expected_amount_raw", "deposit_address"} {
		if _, ok := paymentData[field]; !ok {
			t.Fatalf("types.V1PaymentCreatedData missing property %q", field)
		}
	}

	staticDetail := definitionProperties(t, defs, "types.V1StaticAddressDetail")
	for _, field := range []string{"wallet_id", "user_id", "product_id", "chain", "chain_id", "symbol", "token", "address", "label", "created_at"} {
		if _, ok := staticDetail[field]; !ok {
			t.Fatalf("types.V1StaticAddressDetail missing property %q", field)
		}
	}

	checkoutPayload := definitionProperties(t, defs, "types.PaymentStatusResponse")
	for _, field := range []string{"success", "status", "paid", "payment_id", "tx_hash", "success_path", "cancel_path", "payable", "terminal", "payment_outcome", "payment_outcome_reason", "matched_amount_raw", "shortfall_amount_raw", "excess_amount_raw"} {
		if _, ok := checkoutPayload[field]; !ok {
			t.Fatalf("types.PaymentStatusResponse missing property %q", field)
		}
	}

	paymentDetail := definitionProperties(t, defs, "types.V1PaymentDetail")
	for _, field := range []string{"payment_outcome", "payment_outcome_reason", "matched_amount_raw", "shortfall_amount_raw", "excess_amount_raw"} {
		if _, ok := paymentDetail[field]; !ok {
			t.Fatalf("types.V1PaymentDetail missing property %q", field)
		}
	}

	paymentStats := definitionProperties(t, defs, "types.V1PaymentStatisticsData")
	if _, ok := paymentStats["statuses"]; !ok {
		t.Fatal("types.V1PaymentStatisticsData missing statuses property")
	}
}

// markdownAnchorSection extracts the region between two explicit HTML anchors of a
// translated document. It fails closed: a missing start anchor, a missing end anchor,
// an empty region or an empty tail after the start anchor is an error, so a negative
// assertion can never pass because a lookup silently returned "" or the whole document.
// An empty endID means "to the end of the document", which still requires body content.
func markdownAnchorSection(doc, startID, endID string) (string, error) {
	startMarker := `<a id="` + startID + `"></a>`
	startIndex := strings.Index(doc, startMarker)
	if startIndex < 0 {
		return "", fmt.Errorf("start anchor %q not found", startID)
	}
	body := doc[startIndex+len(startMarker):]
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("section after anchor %q is empty", startID)
	}
	if endID == "" {
		return body, nil
	}
	endMarker := `<a id="` + endID + `"></a>`
	endIndex := strings.Index(body, endMarker)
	if endIndex < 0 {
		return "", fmt.Errorf("end anchor %q not found after %q", endID, startID)
	}
	section := body[:endIndex]
	if strings.TrimSpace(section) == "" {
		return "", fmt.Errorf("section %q..%q is empty", startID, endID)
	}
	return section, nil
}

func requireAnchorSection(t *testing.T, doc, startID, endID string) string {
	t.Helper()
	section, err := markdownAnchorSection(doc, startID, endID)
	if err != nil {
		t.Fatalf("anchor section %s..%s: %v", startID, endID, err)
	}
	return section
}

func TestMarkdownAnchorSectionFailsClosed(t *testing.T) {
	doc := "<a id=\"a\"></a>\n### 中文标题\n正文内容\n<a id=\"b\"></a>\n第二节内容\n<a id=\"c\"></a>\n"
	section, err := markdownAnchorSection(doc, "a", "b")
	if err != nil {
		t.Fatalf("valid section returned error: %v", err)
	}
	if !strings.Contains(section, "中文标题") || strings.Contains(section, "第二节内容") {
		t.Fatalf("section bounds wrong: %q", section)
	}
	if _, err := markdownAnchorSection(doc, "missing", "b"); err == nil {
		t.Fatal("missing start anchor must fail closed")
	}
	if _, err := markdownAnchorSection(doc, "a", "missing"); err == nil {
		t.Fatal("missing end anchor must fail closed")
	}
	if _, err := markdownAnchorSection(doc, "b", "a"); err == nil {
		t.Fatal("reversed anchors must fail closed")
	}
	if section, err := markdownAnchorSection(doc, "b", ""); err != nil || !strings.Contains(section, "第二节内容") {
		t.Fatalf("open-ended section to EOF = %q, err = %v", section, err)
	}
	if _, err := markdownAnchorSection(doc, "c", ""); err == nil {
		t.Fatal("anchor with no body (EOF) must fail closed")
	}
}

func markdownSection(doc, start, end string) string {
	startIndex := strings.Index(doc, start)
	if startIndex < 0 {
		return ""
	}
	section := doc[startIndex:]
	if end == "" {
		return section
	}
	endIndex := strings.Index(section, end)
	if endIndex < 0 {
		return section
	}
	return section[:endIndex]
}

func requireContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected content to contain %q", needle)
	}
}

func requireNotContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("expected content not to contain %q", needle)
	}
}

func asMap(t *testing.T, value any, name string) map[string]any {
	t.Helper()
	m, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object", name)
	}
	return m
}

func operation(t *testing.T, paths map[string]any, path, method string) map[string]any {
	t.Helper()
	pathObj := asMap(t, paths[path], path)
	return asMap(t, pathObj[method], path+" "+method)
}

func requireOperationParam(t *testing.T, operation map[string]any, name string) {
	t.Helper()
	params, ok := operation["parameters"].([]any)
	if !ok {
		t.Fatalf("operation has no parameters")
	}
	for _, param := range params {
		paramObj := asMap(t, param, "parameter")
		if paramObj["name"] == name {
			return
		}
	}
	t.Fatalf("operation missing parameter %q", name)
}

func requireOperationParamEnum(t *testing.T, operation map[string]any, name string, want []string) {
	t.Helper()
	params, ok := operation["parameters"].([]any)
	if !ok {
		t.Fatalf("operation has no parameters")
	}
	for _, param := range params {
		paramObj := asMap(t, param, "parameter")
		if paramObj["name"] != name {
			continue
		}
		rawEnum, ok := paramObj["enum"].([]any)
		if !ok {
			t.Fatalf("parameter %q has no enum", name)
		}
		got := make(map[string]bool, len(rawEnum))
		for _, raw := range rawEnum {
			value, ok := raw.(string)
			if !ok {
				t.Fatalf("parameter %q enum value is not string: %#v", name, raw)
			}
			got[value] = true
		}
		for _, value := range want {
			if !got[value] {
				t.Fatalf("parameter %q enum missing %q from %#v", name, value, got)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("parameter %q enum size = %d, want %d: %#v", name, len(got), len(want), got)
		}
		return
	}
	t.Fatalf("operation missing parameter %q", name)
}

func requireOperationResponse(t *testing.T, operation map[string]any, status, ref string) {
	t.Helper()
	responses := asMap(t, operation["responses"], "responses")
	response := asMap(t, responses[status], "response "+status)
	schema := asMap(t, response["schema"], "response "+status+" schema")
	if schema["$ref"] != ref {
		t.Fatalf("response %s ref = %v, want %s", status, schema["$ref"], ref)
	}
}

func definitionProperties(t *testing.T, definitions map[string]any, name string) map[string]any {
	t.Helper()
	definition := asMap(t, definitions[name], name)
	return asMap(t, definition["properties"], name+" properties")
}
