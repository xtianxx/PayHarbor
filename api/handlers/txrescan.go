package handlers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"core/constants"
	"core/localization"
	"core/models"
	"core/services/txrescan"

	"github.com/gofiber/fiber/v3"
)

const txRescanRequestTimeout = 90 * time.Second

func HandleDealerTxRescan(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectWithError(c, "/merchant/login", portalText(c, "You must sign in to continue."))
		}
		loc := portalLocale(c)
		chainID, txHash, err := parseTxRescanInput(c)
		if err != nil {
			return redirectWithError(c, "/merchant/dashboard/rescan", err.Error())
		}
		service := txRescanServiceFromDealerDeps(deps)
		if service == nil {
			return redirectWithError(c, "/merchant/dashboard/rescan", portalText(c, "Tx rescan service is not ready."))
		}
		ctx, cancel := requestTimeout(c, txRescanRequestTimeout)
		defer cancel()
		result, err := service.RescanForMerchant(ctx, chainID, txHash, merchant.ID)
		if err != nil {
			return redirectWithError(c, "/merchant/dashboard/rescan", txRescanErrorMessage(loc, err))
		}
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "tx.rescan", "success", "transaction", txHash, txRescanSuccessMessage(localization.EN, result))
		return redirectWithSuccess(c, "/merchant/dashboard/rescan", txRescanSuccessMessage(loc, result))
	}
}

func HandleAdminTxRescan(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", portalText(c, "Admin sign in required."))
		}
		loc := portalLocale(c)
		chainID, txHash, err := parseTxRescanInput(c)
		if err != nil {
			return redirectWithError(c, "/admin/rescan", err.Error())
		}
		service := txRescanServiceFromDealerDeps(deps)
		if service == nil {
			return redirectWithError(c, "/admin/rescan", portalText(c, "Tx rescan service is not ready."))
		}
		ctx, cancel := requestTimeout(c, txRescanRequestTimeout)
		defer cancel()
		result, err := service.Rescan(ctx, chainID, txHash)
		if err != nil {
			return redirectWithError(c, "/admin/rescan", txRescanErrorMessage(loc, err))
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "tx.rescan", "success", "transaction", txHash, txRescanSuccessMessage(localization.EN, result))
		return redirectWithSuccess(c, "/admin/rescan", txRescanSuccessMessage(loc, result))
	}
}

// HandleV1TransactionRescan godoc
// @Summary Rescan transaction
// @Description Re-fetches a transaction from the selected blockchain and replays it through wallet matching, payment, ledger, and webhook processing.
// @Tags Transaction
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param X-API-Secret header string true "API secret returned when the domain was created or rotated"
// @Param X-Gateway-Timestamp header string true "Unix timestamp used in HMAC signature"
// @Param X-Gateway-Signature header string true "HMAC-SHA256 over method + path/query + timestamp + raw body, optionally prefixed with sha256="
// @Param payload body map[string]string true "chain and tx_hash"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Failure 404 {object} types.V1ErrorResponse
// @Router /api/v1/transaction/rescan [post]
func HandleV1TransactionRescan(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveSignedDomainForScope(c, deps.DomainRepo, models.DomainAPIScopeTransactionRescan)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		var body struct {
			Chain   string `json:"chain"`
			ChainID string `json:"chain_id"`
			TxHash  string `json:"tx_hash"`
			Hash    string `json:"hash"`
		}
		if err := c.Bind().Body(&body); err != nil {
			return v1Err(c, fiber.StatusBadRequest, "invalid request body")
		}
		chainValue := strings.TrimSpace(body.Chain)
		if chainValue == "" {
			chainValue = strings.TrimSpace(body.ChainID)
		}
		chainID, err := parseTxRescanChain(chainValue)
		if err != nil {
			return v1Err(c, fiber.StatusBadRequest, err.Error())
		}
		txHash := strings.TrimSpace(body.TxHash)
		if txHash == "" {
			txHash = strings.TrimSpace(body.Hash)
		}
		if txHash == "" {
			return v1Err(c, fiber.StatusBadRequest, "tx_hash is required")
		}
		service := txRescanServiceFromV1Deps(deps)
		if service == nil {
			return v1Err(c, fiber.StatusServiceUnavailable, "tx rescan service is not ready")
		}
		ctx, cancel := requestTimeout(c, txRescanRequestTimeout)
		defer cancel()
		result, err := service.RescanForMerchant(ctx, chainID, txHash, domain.MerchantID)
		if err != nil {
			status := fiber.StatusBadRequest
			if errors.Is(err, txrescan.ErrTransactionNotFound) {
				status = fiber.StatusNotFound
			}
			if errors.Is(err, txrescan.ErrUnauthorizedTx) {
				status = fiber.StatusForbidden
			}
			if txRescanTimedOut(err) {
				status = fiber.StatusGatewayTimeout
			}
			return v1Err(c, status, txRescanErrorMessage(localization.EN, err))
		}
		return v1OK(c, fiber.Map{
			"chain_id":              result.ChainID,
			"chain":                 result.Chain,
			"tx_hash":               result.Hash,
			"events":                result.Events,
			"deposits_created":      result.DepositsCreated,
			"deposits_matched":      result.DepositsMatched,
			"deposits_unmatched":    result.DepositsUnmatched,
			"deposits_finalized":    result.DepositsFinalized,
			"transactions_recorded": result.TransactionsRecorded,
			"payments_settled":      result.PaymentsSettled,
			"unique_hashes":         result.UniqueHashes,
		})
	}
}

func txRescanServiceFromDealerDeps(deps DealerDeps) *txrescan.Service {
	if deps.TxRescanService == nil {
		return nil
	}
	return deps.TxRescanService()
}

func txRescanServiceFromV1Deps(deps V1APIDeps) *txrescan.Service {
	if deps.TxRescanService == nil {
		return nil
	}
	return deps.TxRescanService()
}

func parseTxRescanInput(c fiber.Ctx) (constants.ChainID, string, error) {
	chainValue := firstNonEmpty(c.FormValue("chain"), c.FormValue("chain_id"))
	if strings.TrimSpace(chainValue) == "" {
		return 0, "", errors.New(portalText(c, "You must select a network."))
	}
	chainID, err := parseTxRescanChain(chainValue)
	if err != nil {
		return 0, "", errors.New(portalText(c, "Unsupported network: %s", strings.TrimSpace(chainValue)))
	}
	txHash := strings.TrimSpace(firstNonEmpty(c.FormValue("tx_hash"), c.FormValue("hash")))
	if txHash == "" {
		return 0, "", errors.New(portalText(c, "Tx hash is required."))
	}
	return chainID, txHash, nil
}

func parseTxRescanChain(value string) (constants.ChainID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("You must select a network.")
	}
	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
		chainID := constants.ChainID(parsed)
		if constants.IsSupportedChainID(chainID) {
			return chainID, nil
		}
	}
	if chainID := chainSlugToID(value); constants.IsSupportedChainID(chainID) {
		return chainID, nil
	}
	return 0, fmt.Errorf("Unsupported network: %s", value)
}

func txRescanSuccessMessage(loc localization.Locale, result *txrescan.Result) string {
	if result == nil {
		return localization.T(loc, "Transaction rescanned.")
	}
	parts := []string{localization.T(loc, "%s transaction rescanned: %d events processed", result.Chain, result.Events)}
	if result.DepositsMatched > 0 {
		parts = append(parts, localization.T(loc, "%d deposits matched", result.DepositsMatched))
	}
	if result.DepositsUnmatched > 0 {
		parts = append(parts, localization.T(loc, "%d transactions did not match a wallet; no deposits written", result.DepositsUnmatched))
	}
	if result.TransactionsRecorded > 0 {
		parts = append(parts, localization.T(loc, "%d transactions recorded", result.TransactionsRecorded))
	}
	if result.DepositsFinalized > 0 {
		parts = append(parts, localization.T(loc, "%d deposits finalized", result.DepositsFinalized))
	}
	if result.PaymentsSettled > 0 {
		parts = append(parts, localization.T(loc, "%d payments settled", result.PaymentsSettled))
	}
	return strings.Join(parts, ", ") + "."
}

func txRescanErrorMessage(loc localization.Locale, err error) string {
	switch {
	case errors.Is(err, txrescan.ErrTransactionNotFound):
		return localization.T(loc, "Transaction was not found on the blockchain.")
	case errors.Is(err, txrescan.ErrUnauthorizedTx):
		return localization.T(loc, "This transaction does not match the merchant wallet addresses.")
	case errors.Is(err, txrescan.ErrUnsupportedChain):
		return localization.T(loc, "Rescan is not supported for this network.")
	case txRescanTimedOut(err):
		return localization.T(loc, "Rescan timed out. The request was sent, but the blockchain RPC response timed out; completion could not be verified. Try again later.")
	case errors.Is(err, context.Canceled):
		return localization.T(loc, "Rescan was canceled before completion. The transaction could not be verified as complete.")
	default:
		return err.Error()
	}
}

func txRescanTimedOut(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "context deadline exceeded") ||
		strings.Contains(message, "client.timeout") ||
		strings.Contains(message, "timeout awaiting response headers")
}

func requestTimeout(c fiber.Ctx, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Context(), timeout)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
