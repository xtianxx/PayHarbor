package localization

// jsZH holds the browser-side strings shipped in the JSON bundle for portal-i18n.js
// consumers: dashboard.js (and the invoice's standalone script). The keys are the exact
// English source text used at the call site; "%s"/"%d" keep the same verbs, in the same
// order, in the translation.
//
// Vocabulary that the Go surfaces share ("Wallet", "Network", "Search", …) is repeated
// here with the same translation as coreZH so this file stays the single source for the
// browser bundle.
var jsZH = map[string]string{
	// Webhook test tool.
	"Sending...": "发送中...",
	"Test":       "测试",
	"Test message published to the NATS subject successfully.": "测试消息已成功发布到 NATS 主题。",
	"HTTP %s - webhook delivered successfully.":                "HTTP %s - Webhook 投递成功。",
	"Request failed: %s":                                       "请求失败：%s",

	// API secret rotation + copy.
	"The API secret will be rotated. The current secret is revoked immediately. Continue?": "即将轮换 API 签名密钥，当前密钥会立即失效。是否继续？",
	"Rotating...":                       "轮换中...",
	"Rotation request sent.":            "轮换请求已发送。",
	"Rotation failed":                   "轮换失败",
	"New API secret is shown only once": "新的 API 签名密钥仅显示一次",
	"Copy secret":                       "复制密钥",
	"This value is not shown again. Save it to your secret manager; the old secret has been revoked immediately.": "该值不会再次显示，请保存到你的密钥管理器；旧密钥已立即失效。",
	"Copied": "已复制",

	// Network visibility policy.
	"Policy":   "策略",
	"Hidden":   "已隐藏",
	"Visible":  "可见",
	"Locked":   "已锁定",
	"Show":     "显示",
	"Hide":     "隐藏",
	"Show: %s": "显示：%s",
	"Hide: %s": "隐藏：%s",
	"Network":  "网络",
	"network":  "网络",
	"%s is locked by the testnet policy. Turn off the testnet filter first.": "%s 被测试网策略锁定。请先关闭测试网筛选。",
	"%s moved to hidden networks.":                                           "%s 已移至隐藏网络。",
	"%s moved to visible networks.":                                          "%s 已移至可见网络。",
	"A network locked by the testnet policy cannot be moved.":                "被测试网策略锁定的网络无法移动。",
	"Testnet policy enabled.":                                                "测试网策略已启用。",
	"Testnet policy disabled.":                                               "测试网策略已禁用。",

	// Transaction rescan feedback.
	"Processing...": "处理中...",
	"Request sent; waiting for the blockchain RPC response.": "请求已发送；正在等待区块链 RPC 响应。",
	"%s s": "%s 秒",
	"Waiting for the RPC response; the transaction is still in progress.": "正在等待 RPC 响应；交易仍在进行中。",

	// Withdrawal balance panel.
	"Select a reserve wallet and asset":           "选择储备钱包和资产",
	"No available balance for the selected asset": "所选资产没有可用余额",

	// Rich selects (search, empty state, preview labels).
	"Search":     "搜索",
	"No results": "无结果",
	"Domain ID":  "域名 ID",
	"Wallet":     "钱包",
	"Owner":      "所有者",
	"Wallet ID":  "钱包 ID",
	"Select":     "选择",

	// Recover funds (chain/asset/wallet selection and balances).
	"Select asset":                             "选择资产",
	"Select a chain first":                     "请先选择网络",
	"No assets for this chain":                 "此网络没有资产",
	"Select a source wallet with a balance":    "选择有余额的源钱包",
	"No wallet with a balance for this asset":  "此资产没有有余额的钱包",
	"Select a chain and asset first":           "请先选择网络和资产",
	"Select an asset first":                    "请先选择资产",
	"Select wallet and asset":                  "选择钱包和资产",
	"Live chain: -":                            "链上实时：-",
	"Raw: -":                                   "原始值：-",
	"Waiting":                                  "等待中",
	"Live chain: use Refresh to read":          "链上实时：使用刷新读取",
	"Live chain: querying...":                  "链上实时：查询中...",
	"Live chain: could not be read":            "链上实时：无法读取",
	"Live chain: %s %s":                        "链上实时：%s %s",
	"Live balance could not be read":           "无法读取链上实时余额",
	"Unknown error":                            "未知错误",
	"Raw: %s":                                  "原始值：%s",
	"Locked: %s":                               "锁定：%s",
	"Fee raw: %s":                              "手续费原始值：%s",
	"Max raw: %s":                              "最大原始值：%s",
	"Net raw: %s":                              "净额原始值：%s",
	"Net transfer raw: %s":                     "净可转账原始值：%s",
	"Fee could not be read":                    "无法读取手续费",
	"Asset":                                    "资产",
	"Available":                                "可用",
	"Refresh required for fee":                 "手续费需刷新",
	"Locked balance: Refresh required for fee": "锁定的余额：手续费需刷新",
	"Sweep-locked available":                   "归集锁定的可用余额",
	"No balance":                               "无余额",
	"Available after fee":                      "扣除手续费后可用",
	"Locked balance can be transferred":        "锁定余额可转账",
	"No transfer after fee":                    "扣除手续费后无可转账金额",

	// Admin data grid affordances.
	"Pagination":      "分页",
	"Data grid tools": "数据表格工具",
}

// jsCatalogKeys are the catalog keys shipped to the browser: the JSON bundle carries
// exactly this list. Every entry must be covered by the zh catalog (test-enforced),
// otherwise the Chinese UI would silently show English.
var jsCatalogKeys = []string{
	// Webhook test tool.
	"Sending...",
	"Test",
	"Test message published to the NATS subject successfully.",
	"HTTP %s - webhook delivered successfully.",
	"Request failed: %s",

	// API secret rotation + copy.
	"The API secret will be rotated. The current secret is revoked immediately. Continue?",
	"Rotating...",
	"Rotation request sent.",
	"Rotation failed",
	"New API secret is shown only once",
	"Copy secret",
	"This value is not shown again. Save it to your secret manager; the old secret has been revoked immediately.",
	"Copied",

	// Network visibility policy.
	"Policy",
	"Hidden",
	"Visible",
	"Locked",
	"Show",
	"Hide",
	"Show: %s",
	"Hide: %s",
	"Network",
	"network",
	"%s is locked by the testnet policy. Turn off the testnet filter first.",
	"%s moved to hidden networks.",
	"%s moved to visible networks.",
	"A network locked by the testnet policy cannot be moved.",
	"Testnet policy enabled.",
	"Testnet policy disabled.",

	// Transaction rescan feedback.
	"Processing...",
	"Request sent; waiting for the blockchain RPC response.",
	"%s s",
	"Waiting for the RPC response; the transaction is still in progress.",

	// Withdrawal balance panel.
	"Select a reserve wallet and asset",
	"No available balance for the selected asset",

	// Rich selects (search, empty state, preview labels).
	"Search",
	"No results",
	"Domain ID",
	"Wallet",
	"Owner",
	"Wallet ID",
	"Select",

	// Recover funds (chain/asset/wallet selection and balances).
	"Select asset",
	"Select a chain first",
	"No assets for this chain",
	"Select a source wallet with a balance",
	"No wallet with a balance for this asset",
	"Select a chain and asset first",
	"Select an asset first",
	"Select wallet and asset",
	"Live chain: -",
	"Raw: -",
	"Waiting",
	"Live chain: use Refresh to read",
	"Live chain: querying...",
	"Live chain: could not be read",
	"Live chain: %s %s",
	"Live balance could not be read",
	"Unknown error",
	"Raw: %s",
	"Locked: %s",
	"Fee raw: %s",
	"Max raw: %s",
	"Net raw: %s",
	"Net transfer raw: %s",
	"Fee could not be read",
	"Asset",
	"Available",
	"Refresh required for fee",
	"Locked balance: Refresh required for fee",
	"Sweep-locked available",
	"No balance",
	"Available after fee",
	"Locked balance can be transferred",
	"No transfer after fee",

	// Admin data grid affordances.
	"Pagination",
	"Data grid tools",

	// Buyer flow browser strings (pay.html inline script + checkout.js). The translations
	// live in catalog_zh_buyer.go (buyerZH); they are listed here so the JSON bundle
	// carries them.
	"Payment confirmed",
	"Redirecting to the success page.",
	"Payment confirming",
	"A transaction was detected and is waiting for final settlement.",
	"Waiting for payment",
	"Choose the amount in your wallet and send it to the address below.",
	"Send the exact amount to the address below.",
	"Partial payment received",
	"The received amount is being added to this checkout. Send the remaining amount to complete the payment.",
	"Payment",
	"Waiting for payment...",
	"Live connection is active. Send the amount you choose now.",
	"Live connection is active. Send the exact amount now.",
	"Live connection reconnecting",
	"Polling fallback is still checking this payment.",
	"Underpaid",
	"Overpaid",
	"Partial payment",
	"The received amount is below the expected amount.",
	"The received amount is above the expected amount. The merchant may need to review it.",
	"Only part of the expected amount was received.",
	"This checkout can no longer receive a payment.",
	"Choose coin",
	"Waiting for coin",
	"Select network",
}
