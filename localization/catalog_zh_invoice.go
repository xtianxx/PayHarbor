package localization

// invoiceZH holds the invoice page and invoice-handler labels. The hosted checkout keeps
// its own en/tr switching and must never appear here.
//
// Status wording follows the shared vocabulary above (the same key must carry the same
// translation in every area file); nothing may weaken the semantics of a payment state.
var invoiceZH = map[string]string{
	"Canceled":            "已取消",
	"Partial payment":     "部分支付",
	"Waiting for payment": "等待支付",
	"Payment not found":   "未找到支付记录",

	// Shared status vocabulary (identical wording to the dashboard/admin areas).
	"Overpaid":  "超额",
	"Underpaid": "不足",

	// Invoice page copy.
	"Receipt":                "收据",
	"Secure invoice":         "安全发票",
	"Total amount":           "总金额",
	"Paid asset %s":          "已支付资产 %s",
	"Invoice details":        "发票详情",
	"Order":                  "订单",
	"Payment date":           "支付时间",
	"Gateway":                "网关",
	"Chain transaction hash": "链上交易哈希",
	"Invoice link":           "发票链接",
	"Go to payment page":     "前往支付页面",
	"Print / PDF":            "打印 / PDF",
	"Copy link":              "复制链接",
	"Open checkout":          "打开收银台",
}
