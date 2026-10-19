package handlers

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"core/types"
)

func TestIntegrationGuideDocumentsEpic1ContractEvidence(t *testing.T) {
	guide := readIntegrationGuide(t)

	requireGuideContains(t, guide,
		"request_signature_payload: \"METHOD\\npath?query\\ntimestamp\\nraw_body\"",
		"signature_payload: \"timestamp + raw_body\"",
		"message = METHOD + \"\\n\" + path_and_query + \"\\n\" + timestamp + \"\\n\" + body",
		"Idempotency-Key",
		"幂等冲突示例",
		"HMAC 失败示例",
		"timestamp expired",
		"request signature replayed",
		"Webhook 校验示例",
		"docs/money-event-catalog.md",
		"payment.succeeded.v1",
		"native_transfer",
		"合作方 API 契约证据",
		"商户/域",
		"租户/域",
		"生产托管仍受门禁约束",
		"既有的下划线 Webhook 事件名作为兼容别名保留",
	)
	if strings.Contains(guide, "message = timestamp + body") {
		t.Fatal("integration guide still documents stale V1 timestamp+body request signing")
	}
}

func TestIntegrationGuideDocumentsCurrentResponseSchemaKeys(t *testing.T) {
	guide := readIntegrationGuide(t)

	requireJSONFieldsDocumented(t, guide, reflect.TypeOf(types.V1PaymentCreatedData{}))
	requireJSONFieldsDocumented(t, guide, reflect.TypeOf(types.V1StaticAddressDetail{}))
	requireJSONFieldsDocumented(t, guide, reflect.TypeOf(types.PaymentStatusResponse{}))

	requireGuideContains(t, guide,
		"\"result\": \"ok\"",
		"\"data\": {",
		"\"underpaid\"",
		"\"overpaid\"",
		"\"partial_paid\"",
		"\"payable\"",
		"\"terminal\"",
		"\"payment_outcome\"",
		"aggregate_complete",
		"partial_aggregating",
		"missing_memo",
		"wrong_memo",
		"\"matched_amount_raw\"",
		"\"shortfall_amount_raw\"",
		"\"excess_amount_raw\"",
	)
}

func readIntegrationGuide(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "integration-guide.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read integration guide: %v", err)
	}
	return string(b)
}

func requireJSONFieldsDocumented(t *testing.T, guide string, typ reflect.Type) {
	t.Helper()
	for _, field := range jsonFieldNames(typ) {
		requireGuideContains(t, guide, `"`+field+`"`)
	}
}

func jsonFieldNames(typ reflect.Type) []string {
	var names []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		names = append(names, name)
	}
	return names
}

func requireGuideContains(t *testing.T, guide string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(guide, needle) {
			t.Fatalf("integration guide missing %q", needle)
		}
	}
}
