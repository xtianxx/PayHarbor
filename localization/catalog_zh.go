package localization

import "sort"

// The zh-CN catalog is English-keyed and split into one map per surface area so that
// translations can be reviewed and edited per area without conflicts:
//
//	coreZH     — shared shell/table/status vocabulary and this file's documentation
//	shellZH    — layout, header/footer, public and auth pages
//	dashboardZH— merchant dashboard panels, products, domains, settings, rescan
//	adminZH    — admin dashboard, admin login, 2FA pages
//	invoiceZH  — invoice page and invoice handler labels
//	buyerZH    — hosted checkout, pay page, payment result and payer-facing handler copy
//	handlersZH — portal/admin handler copy and flash messages (dealer.go, txrescan.go)
//	jsZH       — browser strings shipped in the JSON bundle (dashboard.js)
//
// Rules for every entry:
//  1. The key is the English source text, byte for byte as it appears in the code.
//  2. Format verbs are positional and their order must match the key (verb-parity test).
//  3. No "#context" discriminator exists yet; it is added only if a real
//     same-text/different-meaning case appears.
//
// The maps are unexported and read-only after initialization: no exported map, no setter.
var catalogZH = mergeCatalogs(coreZH, shellZH, dashboardZH, adminZH, invoiceZH, buyerZH, handlersZH, jsZH)

// coreZH holds the vocabulary shared by several surfaces plus error/status wording.
var coreZH = map[string]string{
	// Glossary terms (supervisor-approved wording).
	"merchant":               "商户",
	"Merchants":              "商户",
	"domain":                 "域名",
	"Domains":                "域名",
	"Deposit":                "入账",
	"Deposits":               "入账",
	"Top up":                 "充值",
	"Top-up":                 "充值",
	"Withdrawal":             "提现",
	"Withdrawals":            "提现",
	"Refund":                 "退款",
	"Refunds":                "退款",
	"Sweep":                  "归集",
	"Sweeps":                 "归集",
	"balance":                "余额",
	"Balance":                "余额",
	"Payment link":           "支付链接",
	"Payment links":          "支付链接",
	"Invoice":                "发票",
	"Invoices":               "发票",
	"API key":                "API 密钥",
	"API secret":             "API 签名密钥",
	"Webhook signing secret": "Webhook 签名密钥",
	"Webhook":                "Webhook",

	// Shell and common table/action labels.
	"PayHarbor Merchant Portal": "PayHarbor 商户门户",
	"Sign in":                   "登录",
	"Sign out":                  "退出登录",
	"Save":                      "保存",
	"Cancel":                    "取消",
	"Delete":                    "删除",
	"Edit":                      "编辑",
	"Search":                    "搜索",
	"Settings":                  "设置",
	"Status":                    "状态",
	"Amount":                    "金额",
	"Date":                      "日期",
	"Created":                   "创建时间",
	"All":                       "全部",
	"Wallet":                    "钱包",
	"Address":                   "地址",
	"Network":                   "网络",
	"Transaction":               "交易",
	"Transactions":              "交易",
	"Two-Factor Authentication": "两步验证",
	"Pending":                   "待处理",
	"Completed":                 "已完成",
	"Failed":                    "失败",
	"Expired":                   "已过期",

	// Format-string examples (verb order must match the key).
	"Invoice #%s":     "发票 #%s",
	"Signed in as %s": "已登录：%s",
	"%d of %d":        "第 %d 项，共 %d 项",
	"100% reserved":   "100% 已预留",
}

// mergeCatalogs flattens the per-area maps. Later areas win on duplicates; the catalog
// test asserts that any key defined in more than one area carries the same translation,
// so duplicated vocabulary cannot drift silently.
func mergeCatalogs(areas ...map[string]string) map[string]string {
	total := 0
	for _, area := range areas {
		total += len(area)
	}
	merged := make(map[string]string, total)
	for _, area := range areas {
		for key, text := range area {
			merged[key] = text
		}
	}
	return merged
}

// lookupText returns the text for a canonical locale and key. English (and any other
// canonical locale) yields the key itself; zh-CN yields the catalog entry or, when the
// key is missing, the English source text.
func lookupText(l Locale, key string) string {
	if key == "" || l != ZHCN {
		return key
	}
	if text, ok := catalogZH[key]; ok {
		return text
	}
	return key
}

// Keys returns a sorted copy of the catalog keys. The copy keeps the internal map
// unmodifiable through the public API.
func Keys() []string {
	keys := make([]string, 0, len(catalogZH))
	for key := range catalogZH {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// JSKeys returns a sorted copy of the keys exported to the browser.
func JSKeys() []string {
	keys := append([]string(nil), jsCatalogKeys...)
	sort.Strings(keys)
	return keys
}
