package repositories

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"time"

	"core/constants"
	"core/models"
	webhooksvc "core/services/webhook"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RefundRepo struct {
	db *gorm.DB
}

func NewRefundRepo(db *gorm.DB) *RefundRepo {
	return &RefundRepo{db: db}
}

func (r *RefundRepo) CountByStatus(ctx context.Context, statuses ...string) (map[string]int64, error) {
	counts := make(map[string]int64, len(statuses))
	for _, status := range statuses {
		counts[status] = 0
	}
	if r == nil || r.db == nil {
		return counts, gorm.ErrInvalidDB
	}
	if len(statuses) == 0 {
		return counts, nil
	}
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Model(&models.Refund{}).
		Select("status, COUNT(*) AS count").
		Where("status IN ?", statuses).
		Group("status").
		Find(&rows).Error; err != nil {
		return counts, err
	}
	for _, row := range rows {
		counts[row.Status] = row.Count
	}
	return counts, nil
}

func (r *RefundRepo) Create(ctx context.Context, refund *models.Refund) error {
	if refund == nil {
		return gorm.ErrInvalidData
	}
	if refund.ID == uuid.Nil {
		refund.ID = uuid.New()
	}
	if refund.Status == "" {
		refund.Status = models.RefundStatusPending
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(refund).Error; err != nil {
			return err
		}
		return recordRefundLifecycleWithDB(ctx, tx, constants.WebhookEventRefundRequestedV1, *refund)
	})
}

func (r *RefundRepo) CreateWithHold(ctx context.Context, refund *models.Refund, session models.PaymentSession, ledger *LedgerRepo) error {
	if ledger == nil {
		return ErrLedgerReservationRequired
	}
	if refund == nil {
		return gorm.ErrInvalidData
	}
	if refund.ID == uuid.Nil {
		refund.ID = uuid.New()
	}
	if refund.Status == "" {
		refund.Status = models.RefundStatusPending
	}
	applyRefundSessionMetadata(refund, session)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(refund).Error; err != nil {
			return err
		}
		if err := NewLedgerRepo(tx).CreateRefundHoldWithDB(ctx, tx, *refund, session); err != nil {
			return err
		}
		return recordRefundLifecycleWithDB(ctx, tx, constants.WebhookEventRefundRequestedV1, *refund)
	})
}

func (r *RefundRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit int) ([]models.Refund, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var refunds []models.Refund
	err := r.db.WithContext(ctx).
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).
		Find(&refunds).Error
	return refunds, err
}

func (r *RefundRepo) ListPage(ctx context.Context, page, limit int, status string) ([]models.Refund, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Model(&models.Refund{})
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var refunds []models.Refund
	err := q.Order("created_at DESC").Limit(limit).Offset((page - 1) * limit).Find(&refunds).Error
	return refunds, total, err
}

func (r *RefundRepo) ListByMerchantPage(ctx context.Context, merchantID uuid.UUID, page, limit int) ([]models.Refund, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	q := r.db.WithContext(ctx).Model(&models.Refund{}).Where("merchant_id = ?", merchantID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var refunds []models.Refund
	err := q.Order("created_at DESC").Limit(limit).Offset((page - 1) * limit).Find(&refunds).Error
	return refunds, total, err
}

func (r *RefundRepo) ListByDomainPage(ctx context.Context, merchantID, domainID uuid.UUID, page, limit int) ([]models.Refund, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	q := r.db.WithContext(ctx).Model(&models.Refund{}).Where("merchant_id = ? AND domain_id = ?", merchantID, domainID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var refunds []models.Refund
	err := q.Order("created_at DESC").Limit(limit).Offset((page - 1) * limit).Find(&refunds).Error
	return refunds, total, err
}

func (r *RefundRepo) Find(ctx context.Context, id uuid.UUID) (*models.Refund, error) {
	var refund models.Refund
	if err := r.db.WithContext(ctx).First(&refund, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &refund, nil
}

func (r *RefundRepo) FindByDomain(ctx context.Context, merchantID, domainID, id uuid.UUID) (*models.Refund, error) {
	var refund models.Refund
	if err := r.db.WithContext(ctx).
		Where("merchant_id = ? AND domain_id = ? AND id = ?", merchantID, domainID, id).
		First(&refund).Error; err != nil {
		return nil, err
	}
	return &refund, nil
}

func (r *RefundRepo) FindByDomainIdempotencyKey(ctx context.Context, merchantID, domainID uuid.UUID, key string) (*models.Refund, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var refund models.Refund
	if err := r.db.WithContext(ctx).
		Where("merchant_id = ? AND domain_id = ? AND idempotency_key = ?", merchantID, domainID, key).
		Order("created_at DESC").
		First(&refund).Error; err != nil {
		return nil, err
	}
	return &refund, nil
}

// RefundCreateGuardedRequest is one caller's intent for a refund creation. The
// payment identity is re-read and re-verified inside the guarded transaction, so
// the caller's copies of merchant, domain and payment are only a claim to check.
type RefundCreateGuardedRequest struct {
	Refund     *models.Refund
	MerchantID uuid.UUID
	DomainID   uuid.UUID
	PaymentID  uuid.UUID
}

// CreateWithHoldGuarded creates a refund with its ledger hold and requested
// lifecycle outbox in one transaction that first locks the payment row FOR UPDATE.
// The payment lock is the serialisation point for same-payment refunds: the cap is
// evaluated against the locked payment and the refunds visible after the lock, so
// two concurrent requests for the same payment cannot both fit under one limit.
//
// The cap rule is RefundLimitRaw (shared with the API pre-check) and is computed
// from persisted facts only; the caller-supplied amount is merely the request. Any
// failure rolls back the refund row, the hold and the outbox event together.
func (r *RefundRepo) CreateWithHoldGuarded(ctx context.Context, req RefundCreateGuardedRequest, ledger *LedgerRepo) error {
	if ledger == nil {
		return ErrLedgerReservationRequired
	}
	if req.Refund == nil {
		return gorm.ErrInvalidData
	}
	if req.MerchantID == uuid.Nil || req.DomainID == uuid.Nil || req.PaymentID == uuid.Nil {
		return ErrRefundPaymentNotRefundable
	}
	requestedRaw, ok := refundPositiveBigInt(req.Refund.AmountRaw)
	if !ok {
		return ErrRefundAmountInvalid
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session models.PaymentSession
		if err := tx.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&session, "id = ?", req.PaymentID).Error; err != nil {
			return err
		}
		if session.MerchantID != req.MerchantID || session.DomainID != req.DomainID {
			return ErrRefundPaymentNotRefundable
		}
		if session.Status != models.PaymentStatusPaid {
			return ErrRefundPaymentNotRefundable
		}
		if session.SelectedChainID == nil || !constants.IsSupportedChainID(*session.SelectedChainID) {
			return ErrRefundPaymentNotRefundable
		}

		refund := req.Refund
		if status := strings.TrimSpace(refund.Status); status != "" && status != models.RefundStatusPending {
			return ErrRefundCreateStatusInvalid
		}
		chainID, symbol, decimals, err := ledgerRefundAssetFromSession(session)
		if err != nil {
			return ErrRefundPaymentNotRefundable
		}
		if err := refundAssetMatchesSession(*refund, session, chainID, symbol, decimals); err != nil {
			return err
		}
		facts, err := refundCapFactsWithDB(ctx, tx, session)
		if err != nil {
			return err
		}
		limitRaw, err := RefundLimitRaw(facts)
		if err != nil {
			return err
		}
		activeRaw, err := activeRefundTotalRawWithDB(ctx, tx, session.ID)
		if err != nil {
			return err
		}
		if new(big.Int).Add(activeRaw, requestedRaw).Cmp(limitRaw) > 0 {
			return ErrRefundLimitExceeded
		}

		// A7-R1 non-blocking asset guard. The deposit/reorg path takes the ledger
		// asset lock before it locks a payment row, so this path — which holds the
		// payment row first — must never wait for the asset. If the asset is busy the
		// transaction returns immediately and rolls back (releasing the payment row)
		// instead of joining a lock cycle; the hold below re-enters the same lock in
		// this transaction and therefore does not block.
		assetFree, err := NewLedgerRepo(tx).tryLockLedgerAsset(ctx, tx, session.MerchantID, &session.DomainID, chainID, session.SelectedToken)
		if err != nil {
			return err
		}
		if !assetFree {
			return ErrRefundAssetBusy
		}
		// Server-owned canonical values (A7-R2): the guard stores exactly what it
		// validated, so no caller spelling can escape the active-total rule
		// (`amount_raw ~ '^[0-9]+$'`), no status variant can bypass the pending
		// lifecycle, and the recorded asset is always the locked session's asset.
		if refund.ID == uuid.Nil {
			refund.ID = uuid.New()
		}
		refund.MerchantID = session.MerchantID
		refund.DomainID = session.DomainID
		refund.PaymentID = session.ID
		refund.AmountRaw = requestedRaw.String()
		refund.Status = models.RefundStatusPending
		refund.Chain = constants.ChainName(chainID)
		refund.Token = normalizedOptionalString(session.SelectedToken)
		refund.Symbol = symbol
		refund.Decimals = decimals
		if err := tx.Create(refund).Error; err != nil {
			return err
		}
		if err := NewLedgerRepo(tx).CreateRefundHoldWithDB(ctx, tx, *refund, session); err != nil {
			return err
		}
		return recordRefundLifecycleWithDB(ctx, tx, constants.WebhookEventRefundRequestedV1, *refund)
	})
}

// refundAssetMatchesSession rejects a request that describes a different asset
// than the locked payment session. Empty fields are replaced by the session's
// canonical values before the write; a non-empty disagreement is refused so a
// correct hold can never be recorded against another asset. Token identifiers are
// chain-specific (base58 and hex both occur), so a non-empty token must match the
// session exactly after trimming — never case-insensitively.
func refundAssetMatchesSession(refund models.Refund, session models.PaymentSession, chainID constants.ChainID, symbol string, decimals uint8) error {
	if value := strings.TrimSpace(refund.Chain); value != "" && !strings.EqualFold(value, constants.ChainName(chainID)) {
		return ErrRefundAssetMismatch
	}
	sessionToken := ""
	if session.SelectedToken != nil {
		sessionToken = strings.TrimSpace(*session.SelectedToken)
	}
	if refund.Token != nil {
		if value := strings.TrimSpace(*refund.Token); value != "" && value != sessionToken {
			return ErrRefundAssetMismatch
		}
	}
	if value := strings.TrimSpace(refund.Symbol); value != "" && !strings.EqualFold(value, strings.TrimSpace(symbol)) {
		return ErrRefundAssetMismatch
	}
	if refund.Decimals != 0 && refund.Decimals != decimals {
		return ErrRefundAssetMismatch
	}
	return nil
}

// refundCapFactsWithDB gathers the cap facts inside the caller's transaction. An
// unresolvable related transaction falls back to the expected/matched limit, the
// same branch the API pre-check takes, so the guard never grants a larger cap than
// the rule allows.
func refundCapFactsWithDB(ctx context.Context, tx *gorm.DB, session models.PaymentSession) (RefundCapFacts, error) {
	facts := RefundCapFacts{
		DonationLink:      models.IsDonationLinkType(session.LinkType),
		ExpectedAmountRaw: session.ExpectedAmountRaw,
		MatchedAmountRaw:  session.MatchedAmountRaw,
	}
	if session.TxUniqueHash != nil {
		facts.TxUniqueHash = strings.TrimSpace(*session.TxUniqueHash)
	}
	if facts.TxUniqueHash == "" {
		return facts, nil
	}
	var txModel models.Transaction
	err := tx.WithContext(ctx).Select("amount").First(&txModel, "unique_hash = ?", facts.TxUniqueHash).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return facts, nil
		}
		return RefundCapFacts{}, err
	}
	facts.TxResolved = true
	facts.TxAmountRaw = txModel.Amount
	return facts, nil
}

func activeRefundTotalRawWithDB(ctx context.Context, tx *gorm.DB, paymentID uuid.UUID) (*big.Int, error) {
	var raw string
	err := tx.WithContext(ctx).
		Model(&models.Refund{}).
		Select("COALESCE(SUM(amount_raw::numeric), 0)::text").
		Where("payment_id = ? AND status IN ?", paymentID, refundActiveStatuses()).
		Where("amount_raw ~ '^[0-9]+$'").
		Scan(&raw).Error
	if err != nil {
		return nil, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "0"
	}
	total, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return nil, gorm.ErrInvalidData
	}
	return total, nil
}

func (r *RefundRepo) ActiveTotalRawByPayment(ctx context.Context, paymentID uuid.UUID) (*big.Int, error) {
	return activeRefundTotalRawWithDB(ctx, r.db, paymentID)
}

func (r *RefundRepo) SumActiveAmountRawByMerchantSince(ctx context.Context, merchantID uuid.UUID, chain string, token *string, symbol string, since time.Time) (*big.Int, error) {
	type amountRow struct {
		AmountRaw string
	}
	query := r.db.WithContext(ctx).
		Model(&models.Refund{}).
		Select("amount_raw").
		Where("merchant_id = ? AND created_at >= ? AND status IN ?", merchantID, since, []string{
			models.RefundStatusPending,
			models.RefundStatusProcessing,
			models.RefundStatusApproved,
			models.RefundStatusSucceeded,
		}).
		Where("LOWER(chain) = ?", strings.ToLower(strings.TrimSpace(chain)))

	tokenValue := ""
	if token != nil {
		tokenValue = strings.TrimSpace(*token)
	}
	if tokenValue != "" {
		query = query.Where("LOWER(COALESCE(token, '')) = ?", strings.ToLower(tokenValue))
	} else {
		query = query.Where("COALESCE(token, '') = ''")
		if strings.TrimSpace(symbol) != "" {
			query = query.Where("UPPER(symbol) = ?", strings.ToUpper(strings.TrimSpace(symbol)))
		}
	}

	var rows []amountRow
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	total := big.NewInt(0)
	for _, row := range rows {
		value, ok := new(big.Int).SetString(strings.TrimSpace(row.AmountRaw), 10)
		if !ok || value.Sign() < 0 {
			return nil, gorm.ErrInvalidData
		}
		total.Add(total, value)
	}
	return total, nil
}

func (r *RefundRepo) ListProcessingWithTxHash(ctx context.Context, limit int) ([]models.Refund, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var refunds []models.Refund
	err := r.db.WithContext(ctx).
		Where("status = ?", models.RefundStatusProcessing).
		Where("tx_hash <> ''").
		Order("updated_at ASC").
		Limit(limit).
		Find(&refunds).Error
	return refunds, err
}

func (r *RefundRepo) ClaimPending(ctx context.Context, id uuid.UUID, reviewedBy string) (*models.Refund, error) {
	var claimed models.Refund
	now := time.Now()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&refund, "id = ? AND status = ?", id, models.RefundStatusPending).Error; err != nil {
			return err
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status = ?", id, models.RefundStatusPending).
			Updates(map[string]any{
				"status":      models.RefundStatusProcessing,
				"reviewed_by": reviewedBy,
				"reviewed_at": &now,
				"error":       "",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		refund.Status = models.RefundStatusProcessing
		refund.ReviewedBy = reviewedBy
		refund.ReviewedAt = &now
		refund.Error = ""
		claimed = refund
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &claimed, nil
}

func (r *RefundRepo) ClaimPendingWithHold(ctx context.Context, id uuid.UUID, reviewedBy string, session models.PaymentSession, ledger *LedgerRepo) (*models.Refund, error) {
	return r.ClaimPendingWithHoldAndSource(ctx, id, reviewedBy, session, models.Wallet{}, "", ledger)
}

func (r *RefundRepo) ClaimPendingWithHoldAndSource(ctx context.Context, id uuid.UUID, reviewedBy string, session models.PaymentSession, sourceWallet models.Wallet, toAddress string, ledger *LedgerRepo) (*models.Refund, error) {
	if ledger == nil {
		return nil, ErrLedgerReservationRequired
	}
	var claimed models.Refund
	now := time.Now()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&refund, "id = ? AND status = ?", id, models.RefundStatusPending).Error; err != nil {
			return err
		}
		applyRefundSessionMetadata(&refund, session)
		walletID := sourceWallet.ID
		if walletID != uuid.Nil {
			refund.WalletID = &walletID
		}
		if err := NewLedgerRepo(tx).CreateRefundHoldWithDB(ctx, tx, refund, session); err != nil {
			return err
		}
		if walletID != uuid.Nil {
			if err := NewLedgerRepo(tx).AlignRefundHoldWalletWithDB(ctx, tx, refund.ID, walletID); err != nil {
				return err
			}
		}
		updates := map[string]any{
			"status":      models.RefundStatusProcessing,
			"reviewed_by": reviewedBy,
			"reviewed_at": &now,
			"chain":       refund.Chain,
			"token":       refund.Token,
			"symbol":      refund.Symbol,
			"decimals":    refund.Decimals,
			"to_address":  strings.TrimSpace(toAddress),
			"error":       "",
		}
		if walletID != uuid.Nil {
			updates["wallet_id"] = &walletID
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status = ?", id, models.RefundStatusPending).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		refund.Status = models.RefundStatusProcessing
		refund.ReviewedBy = reviewedBy
		refund.ReviewedAt = &now
		refund.ToAddress = strings.TrimSpace(toAddress)
		refund.Error = ""
		claimed = refund
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &claimed, nil
}

func (r *RefundRepo) ClaimPendingWithHoldAndSourceForOutbound(ctx context.Context, id uuid.UUID, reviewedBy string, session models.PaymentSession, sourceWallet models.Wallet, toAddress string, ledger *LedgerRepo, outbound *OutboundTransactionRepo) (*models.Refund, *models.OutboundTransaction, error) {
	if ledger == nil {
		return nil, nil, ErrLedgerReservationRequired
	}
	if outbound == nil {
		return nil, nil, ErrOutboundTransactionInvalid
	}
	var claimed models.Refund
	var outboundTx *models.OutboundTransaction
	now := time.Now()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&refund, "id = ? AND status = ?", id, models.RefundStatusPending).Error; err != nil {
			return err
		}
		applyRefundSessionMetadata(&refund, session)
		walletID := sourceWallet.ID
		if walletID != uuid.Nil {
			refund.WalletID = &walletID
		}
		if err := NewLedgerRepo(tx).CreateRefundHoldWithDB(ctx, tx, refund, session); err != nil {
			return err
		}
		if walletID != uuid.Nil {
			if err := NewLedgerRepo(tx).AlignRefundHoldWalletWithDB(ctx, tx, refund.ID, walletID); err != nil {
				return err
			}
		}
		refund.ToAddress = strings.TrimSpace(toAddress)
		updates := map[string]any{
			"status":      models.RefundStatusProcessing,
			"reviewed_by": reviewedBy,
			"reviewed_at": &now,
			"chain":       refund.Chain,
			"token":       refund.Token,
			"symbol":      refund.Symbol,
			"decimals":    refund.Decimals,
			"to_address":  refund.ToAddress,
			"error":       "",
		}
		if walletID != uuid.Nil {
			updates["wallet_id"] = &walletID
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status = ?", id, models.RefundStatusPending).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		refund.Status = models.RefundStatusProcessing
		refund.ReviewedBy = reviewedBy
		refund.ReviewedAt = &now
		refund.Error = ""

		created, _, err := NewOutboundTransactionRepo(tx).CreateForRefund(ctx, refund, reviewedBy)
		if err != nil {
			return err
		}
		outboundTx = created
		claimed = refund
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return &claimed, outboundTx, nil
}

func (r *RefundRepo) RecordBroadcast(ctx context.Context, id uuid.UUID, reviewedBy string, txHash string) error {
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return ErrTxHashRequired
	}
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&refund, "id = ? AND status = ?", id, models.RefundStatusProcessing).Error; err != nil {
			return err
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status = ?", id, models.RefundStatusProcessing).
			Updates(map[string]any{
				"reviewed_by":    reviewedBy,
				"reviewed_at":    &now,
				"tx_hash":        txHash,
				"broadcasted_at": &now,
				"error":          "",
				"updated_at":     now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		refund.ReviewedBy = reviewedBy
		refund.ReviewedAt = &now
		refund.TxHash = txHash
		refund.BroadcastedAt = &now
		refund.Error = ""
		refund.UpdatedAt = now
		return recordRefundLifecycleWithDB(ctx, tx, constants.WebhookEventRefundBroadcastV1, refund)
	})
}

func applyRefundSessionMetadata(refund *models.Refund, session models.PaymentSession) {
	if refund == nil || session.SelectedChainID == nil {
		return
	}
	if strings.TrimSpace(refund.Chain) == "" {
		refund.Chain = constants.ChainName(*session.SelectedChainID)
	}
	if refund.Token == nil {
		refund.Token = session.SelectedToken
	}
	if strings.TrimSpace(refund.Symbol) == "" {
		refund.Symbol = strings.ToUpper(strings.TrimSpace(session.SelectedSymbol))
	}
	if strings.TrimSpace(refund.Symbol) == "" {
		refund.Symbol = strings.ToUpper(constants.ChainName(*session.SelectedChainID))
	}
	if refund.Decimals == 0 {
		refund.Decimals = session.SelectedDecimals
	}
}

func (r *RefundRepo) MarkSucceeded(ctx context.Context, id uuid.UUID, reviewedBy string, txHash string) error {
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return ErrTxHashRequired
	}
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		statuses := []string{models.RefundStatusProcessing, models.RefundStatusApproved}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&refund, "id = ? AND status IN ?", id, statuses).Error; err != nil {
			return err
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status IN ?", id, statuses).
			Updates(map[string]any{
				"status":       models.RefundStatusSucceeded,
				"reviewed_by":  reviewedBy,
				"reviewed_at":  &now,
				"tx_hash":      txHash,
				"finalized_at": &now,
				"error":        "",
				"updated_at":   now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		refund.Status = models.RefundStatusSucceeded
		refund.ReviewedBy = reviewedBy
		refund.ReviewedAt = &now
		refund.TxHash = txHash
		refund.FinalizedAt = &now
		refund.Error = ""
		refund.UpdatedAt = now
		return recordRefundLifecycleWithDB(ctx, tx, constants.WebhookEventRefundSucceededV1, refund)
	})
}

func (r *RefundRepo) MarkSucceededWithLedger(ctx context.Context, id uuid.UUID, reviewedBy string, txHash string, session models.PaymentSession, ledger *LedgerRepo) error {
	if ledger == nil {
		return ErrLedgerReservationRequired
	}
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return ErrTxHashRequired
	}
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			First(&refund, "id = ? AND status IN ?", id, []string{models.RefundStatusProcessing, models.RefundStatusApproved}).Error; err != nil {
			return err
		}
		if err := NewLedgerRepo(tx).PostRefundDebitWithDB(ctx, tx, refund, session, txHash); err != nil {
			return err
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status IN ?", id, []string{models.RefundStatusProcessing, models.RefundStatusApproved}).
			Updates(map[string]any{
				"status":       models.RefundStatusSucceeded,
				"reviewed_by":  reviewedBy,
				"reviewed_at":  &now,
				"tx_hash":      txHash,
				"finalized_at": &now,
				"error":        "",
				"updated_at":   now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		refund.Status = models.RefundStatusSucceeded
		refund.ReviewedBy = reviewedBy
		refund.ReviewedAt = &now
		refund.TxHash = txHash
		refund.FinalizedAt = &now
		refund.Error = ""
		refund.UpdatedAt = now
		return recordRefundLifecycleWithDB(ctx, tx, constants.WebhookEventRefundSucceededV1, refund)
	})
}

func (r *RefundRepo) MarkRejected(ctx context.Context, id uuid.UUID, reviewedBy string, reason string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&refund, "id = ? AND status = ?", id, models.RefundStatusPending).Error; err != nil {
			return err
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status = ?", id, models.RefundStatusPending).
			Updates(map[string]any{
				"status":      models.RefundStatusRejected,
				"reviewed_by": reviewedBy,
				"reviewed_at": &now,
				"error":       reason,
				"updated_at":  now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := NewLedgerRepo(tx).VoidRefundHoldWithDB(ctx, tx, id); err != nil {
			return err
		}
		refund.Status = models.RefundStatusRejected
		refund.ReviewedBy = reviewedBy
		refund.ReviewedAt = &now
		refund.Error = reason
		refund.UpdatedAt = now
		return recordRefundLifecycleWithDB(ctx, tx, constants.WebhookEventRefundRejectedV1, refund)
	})
}

func (r *RefundRepo) MarkFailed(ctx context.Context, id uuid.UUID, reviewedBy string, errText string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&refund, "id = ? AND status IN ?", id, []string{models.RefundStatusPending, models.RefundStatusProcessing, models.RefundStatusApproved}).Error; err != nil {
			return err
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status IN ?", id, []string{models.RefundStatusPending, models.RefundStatusProcessing, models.RefundStatusApproved}).
			Updates(map[string]any{
				"status":      models.RefundStatusFailed,
				"reviewed_by": reviewedBy,
				"reviewed_at": &now,
				"error":       errText,
				"updated_at":  now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if strings.TrimSpace(refund.TxHash) == "" {
			if err := NewLedgerRepo(tx).VoidRefundHoldWithDB(ctx, tx, id); err != nil {
				return err
			}
		}
		refund.Status = models.RefundStatusFailed
		refund.ReviewedBy = reviewedBy
		refund.ReviewedAt = &now
		refund.Error = errText
		refund.UpdatedAt = now
		return recordRefundLifecycleWithDB(ctx, tx, constants.WebhookEventRefundFailedV1, refund)
	})
}

func (r *RefundRepo) MarkFailedFinalWithLedgerRelease(ctx context.Context, id uuid.UUID, reviewedBy string, errText string, ledger *LedgerRepo) error {
	if ledger == nil {
		return ErrLedgerReservationRequired
	}
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			First(&refund, "id = ? AND status IN ? AND tx_hash <> ''", id, []string{models.RefundStatusProcessing, models.RefundStatusApproved}).Error; err != nil {
			return err
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status IN ?", id, []string{models.RefundStatusProcessing, models.RefundStatusApproved}).
			Updates(map[string]any{
				"status":       models.RefundStatusFailed,
				"reviewed_by":  reviewedBy,
				"reviewed_at":  &now,
				"finalized_at": &now,
				"error":        errText,
				"updated_at":   now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := NewLedgerRepo(tx).VoidRefundHoldWithDB(ctx, tx, id); err != nil {
			return err
		}
		refund.Status = models.RefundStatusFailed
		refund.ReviewedBy = reviewedBy
		refund.ReviewedAt = &now
		refund.FinalizedAt = &now
		refund.Error = errText
		refund.UpdatedAt = now
		return recordRefundLifecycleWithDB(ctx, tx, constants.WebhookEventRefundFailedV1, refund)
	})
}

func (r *RefundRepo) SetProcessingError(ctx context.Context, id uuid.UUID, errText string) error {
	result := r.db.WithContext(ctx).Model(&models.Refund{}).
		Where("id = ? AND status = ?", id, models.RefundStatusProcessing).
		Update("error", errText)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func recordRefundLifecycleWithDB(ctx context.Context, tx *gorm.DB, eventType string, refund models.Refund) error {
	payload := webhooksvc.NewRefundPayload(eventType, refund)
	_, _, err := NewMoneyEventOutboxRepo(tx).RecordLifecycleWithDB(ctx, tx, payload)
	return err
}
