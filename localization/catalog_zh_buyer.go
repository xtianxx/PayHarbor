package localization

// buyerZH holds the zh-CN copy of the public buyer flow: the hosted checkout, the pay
// page, the payment-result page and the payer-facing strings the payment handlers build
// (status titles/bodies, result labels, receipt labels, payer errors).
//
// The keys follow the same rules as every other surface: the key is the English source
// text exactly as it appears in the template, handler or browser asset; format verbs are
// positional and keep the same order in the translation. Keys that the portal already
// defines ("Order", "Status", "Copy", "Canceled", …) are reused from those maps instead
// of being repeated here, and the buyer browser strings are additionally listed in
// jsCatalogKeys because pay.html's inline script and checkout.js read them from the JSON
// bundle.
var buyerZH = map[string]string{
	// Checkout shell: header, language control and dialog.
	"Secure checkout":                      "安全结账",
	"Checkout progress":                    "结账进度",
	"Change language, current language %s": "更改语言，当前语言：%s",
	"Checkout language":                    "结账语言",
	"Choose a language":                    "选择语言",
	"Close language selection":             "关闭语言选择",
	"Available languages":                  "可用语言",
	"Switch language":                      "切换语言",

	// Checkout order panel.
	"Amount you want to deposit": "充值金额由你决定",
	"You pay":                    "应付金额",
	"Set in wallet":              "在钱包中设定",
	"No expiry":                  "无有效期",

	// Checkout asset/network picker.
	"Payment method":      "支付方式",
	"Choose coin":         "选择币种",
	"Change":              "更换",
	"Search coin":         "搜索币种",
	"1 network":           "1 个网络",
	"%d networks":         "%d 个网络",
	"Select network":      "选择网络",
	"Waiting for token":   "等待选择代币",
	"Waiting for coin":    "等待选择币种",
	"Try again":           "重试",
	"Address unavailable": "地址不可用",
	"Unavailable":         "不可用",
	"Change asset":        "更换资产",
	"No network is available for this token.":                                        "该代币暂无可用网络。",
	"No payment assets are available for this checkout. Please try again shortly.":   "本次结账暂无可用的支付资产，请稍后重试。",
	"No payment networks are available for this checkout. Please try again shortly.": "本次结账暂无可用的支付网络，请稍后重试。",

	// Checkout summary.
	"Price source":    "价格来源",
	"Not used":        "不使用",
	"Gateway pricing": "网关定价",
	"Payer decides":   "由付款方决定",

	// Pay page: amount summary, instructions and warnings.
	"Send your chosen amount": "发送你选择的金额",
	"Send exactly":            "请精确发送",
	"Chosen amount":           "你选择的金额",
	"Wallet address":          "钱包地址",
	"Exact amount":            "精确金额",
	"Payment URI":             "支付 URI",
	"Send only":               "请仅发送",
	"and":                     "和",
	"Sending other assets may result in permanent loss.": "发送其他资产可能导致永久损失。",
	"Copy address":    "复制地址",
	"Payment QR":      "支付二维码",
	"Expected amount": "应付金额",

	// Payer-facing status copy from the payment handlers.
	"Payment received successfully.":                                                        "已收到付款。",
	"This checkout was canceled.":                                                           "本次结账已取消。",
	"The payment could not be completed.":                                                   "支付未能完成。",
	"Waiting for asset selection":                                                           "等待选择资产",
	"Choose an asset and network to continue.":                                              "请选择资产与网络以继续。",
	"The received amount is being aggregated toward this checkout.":                         "已收到的金额正在累计到本次结账。",
	"The received amount partially covers this checkout and needs follow-up.":               "已收到的金额仅覆盖本次结账的一部分，需要后续处理。",
	"This checkout can no longer receive a payment.":                                        "本次结账已无法再接收付款。",
	"The received amount exceeds the expected amount.":                                      "已收到的金额高于应付金额。",
	"The received amount is below the expected amount.":                                     "已收到的金额低于应付金额。",
	"The received amount is above the expected amount. The merchant may need to review it.": "已收到的金额高于应付金额，商户可能需要复核。",
	"Only part of the expected amount was received.":                                        "仅收到应付金额的一部分。",

	// Payment-result page labels.
	"Action required":     "需要处理",
	"Paid amount":         "已支付金额",
	"Settled with":        "结算资产",
	"Transaction details": "交易详情",
	"Receipt references":  "回执参考",
	"Print receipt":       "打印回执",
	"Back to checkout":    "返回结账",

	// Buyer-facing handler titles/labels and payer errors.
	"Track ID":                                      "跟踪 ID",
	"Order ID":                                      "订单号",
	"Payment unavailable":                           "支付不可用",
	"Payment link unavailable":                      "支付链接不可用",
	"Payment canceled":                              "支付已取消",
	"Payment complete":                              "支付完成",
	"The checkout session was canceled.":            "结账会话已取消。",
	"Could not cancel this checkout.":               "无法取消本次结账。",
	"Payment link infrastructure is not ready.":     "支付链接服务尚未就绪。",
	"Product information could not be prepared: %s": "无法准备商品信息：%s",
	"Default asset could not be selected: %s":       "无法选择默认资产：%s",
	"Wallet could not be created: %s":               "无法创建钱包：%s",
	"Checkout could not be created: %s":             "无法创建结账：%s",
	"Payment session was not found.":                "未找到支付会话。",
	"Could not expire this checkout session.":       "无法使本次结账失效。",
	"This payment session has expired.":             "本次支付会话已过期。",
	"Select a valid network.":                       "请选择有效的网络。",
	"Network availability could not be verified. Please try again shortly.": "无法确认网络可用性，请稍后重试。",
	"This network is temporarily under maintenance.":                        "该网络正在维护中。",
	"Deposits are temporarily disabled for this network.":                   "该网络的入账已暂时停用。",

	// Buyer browser strings (pay.html inline script and checkout.js). Their translations
	// live here; jsCatalogKeys lists them for the JSON bundle.
	"Payment confirmed":                "支付已确认",
	"Redirecting to the success page.": "正在跳转到成功页面。",
	"Payment confirming":               "支付确认中",
	"A transaction was detected and is waiting for final settlement.": "已检测到交易，正在等待最终确认。",
	"Waiting for payment": "等待支付",
	"Choose the amount in your wallet and send it to the address below.":                                      "请在钱包中自行选择金额，并发送到下方地址。",
	"Send the exact amount to the address below.":                                                             "请将精确金额发送到下方地址。",
	"Partial payment received":                                                                                "已收到部分付款",
	"The received amount is being added to this checkout. Send the remaining amount to complete the payment.": "已收到的金额正在累计到本次结账，请发送剩余金额以完成支付。",
	"Payment":                "支付",
	"Waiting for payment...": "等待支付...",
	"Live connection is active. Send the amount you choose now.": "实时连接已建立，请立即发送你选择的金额。",
	"Live connection is active. Send the exact amount now.":      "实时连接已建立，请立即发送精确金额。",
	"Live connection reconnecting":                               "实时连接重连中",
	"Polling fallback is still checking this payment.":           "备用轮询仍在检查支付状态。",
}
