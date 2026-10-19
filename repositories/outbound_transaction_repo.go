package repositories

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrOutboundTransactionInvalid      = errors.New("invalid outbound transaction")
	ErrOutboundResourceAlreadyReserved = errors.New("outbound chain resource already reserved")
)

// errSendGrantAttemptTransitionLost is a private sentinel: the grant transaction
// must fail (rolling back its reservation insert) when the guarded attempt
// transition does not apply. Callers outside the transaction map it to a block.
var errSendGrantAttemptTransitionLost = errors.New("outbound send grant attempt transition did not apply")

// Outbound send safety invariant (A6).
//
// A row may be handed to the broadcast executor only when it is a clean first
// attempt: no persisted transaction hash, no broadcast attempt, no
// broadcast/finality timestamp and no terminal error category. Everything else
// is an uncertain send (broadcast_attempted), already broadcast, or terminally
// failed; those rows are resolvable only through operator reconciliation and
// MUST NOT be re-sent automatically.
var outboundTerminalErrorCategories = map[string]struct{}{
	"terminal_failed":               {},
	"broadcast_uncertain":           {},
	"replacement_requires_operator": {},
}

func outboundTerminalErrorCategory(category string) bool {
	_, terminal := outboundTerminalErrorCategories[strings.ToLower(strings.TrimSpace(category))]
	return terminal
}

// outboundTerminalCategoryGuardSQL excludes rows that already carry terminal
// broadcast evidence. It is applied to every status, not only to `failed`: a
// prepared/signed row that somehow carries a terminal category must never be
// sent, and lifecycle writes must not erase that evidence.
const outboundTerminalCategoryGuardSQL = `(error_category IS NULL OR btrim(error_category) = ''
	OR lower(btrim(error_category)) NOT IN ('terminal_failed', 'broadcast_uncertain', 'replacement_requires_operator'))`

// outboundClaimableForSend is the Go mirror of outboundSendableClaimSQL; the two
// must stay in sync.
func outboundClaimableForSend(row models.OutboundTransaction) bool {
	if outboundTerminalErrorCategory(row.ErrorCategory) {
		return false
	}
	if strings.TrimSpace(row.TxHash) != "" || row.BroadcastAttemptedAt != nil ||
		row.BroadcastedAt != nil || row.FinalizedAt != nil {
		return false
	}
	switch row.Status {
	case models.OutboundStatusPrepared, models.OutboundStatusSigned, models.OutboundStatusFailed:
		return row.Status != models.OutboundStatusFinalized
	default:
		return false
	}
}

// outboundSendableClaimSQL restricts a claim to rows outboundClaimableForSend
// would accept: prepared/signed, or a failed pre-broadcast attempt that can be
// retried, and never anything carrying hash/attempt/broadcast/finality or
// terminal-category evidence.
const outboundSendableClaimSQL = `(
	status IN ('prepared', 'signed', 'failed')
	AND ` + outboundTerminalCategoryGuardSQL + `
)
AND tx_hash = ''
AND broadcast_attempted_at IS NULL
AND broadcasted_at IS NULL
AND finalized_at IS NULL`

// outboundBlockingEvidenceSQL matches another intent of the same resource that
// already carries broadcast evidence; a second intent must then never send.
const outboundBlockingEvidenceSQL = `(
	status IN ('broadcast_attempted', 'broadcasted', 'confirming', 'finalized')
	OR tx_hash <> ''
	OR broadcast_attempted_at IS NOT NULL
	OR broadcasted_at IS NOT NULL
	OR (status = 'failed' AND lower(btrim(coalesce(error_category, ''))) = 'terminal_failed')
)`

type OutboundTransactionRepo struct {
	db *gorm.DB
}

func NewOutboundTransactionRepo(db *gorm.DB) *OutboundTransactionRepo {
	return &OutboundTransactionRepo{db: db}
}

func (r *OutboundTransactionRepo) DB() *gorm.DB { return r.db }

func (r *OutboundTransactionRepo) CountByStatus(ctx context.Context, statuses ...string) (map[string]int64, error) {
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
		Model(&models.OutboundTransaction{}).
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

type OutboundTransactionCreate struct {
	IdempotencyKey string
	ResourceType   string
	ResourceID     uuid.UUID
	MerchantID     uuid.UUID
	DomainID       *uuid.UUID
	WalletID       uuid.UUID
	ChainID        constants.ChainID
	ChainName      string
	Token          *string
	Symbol         string
	Decimals       uint8
	AmountRaw      string
	ToAddress      string
	FeePolicyJSON  string
	ActorID        string
	CorrelationID  string
	NextRunAt      *time.Time
	MaxAttempts    uint
}

func (r *OutboundTransactionRepo) Create(ctx context.Context, params OutboundTransactionCreate) (*models.OutboundTransaction, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	prepared, err := prepareOutboundTransaction(params)
	if err != nil {
		return nil, false, err
	}

	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&prepared)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return &prepared, true, nil
	}

	var existing models.OutboundTransaction
	if err := r.db.WithContext(ctx).First(&existing, "idempotency_key = ?", prepared.IdempotencyKey).Error; err != nil {
		return nil, false, err
	}
	return &existing, false, nil
}

func (r *OutboundTransactionRepo) CreateForWithdrawal(ctx context.Context, request models.WithdrawalRequest, actorID string) (*models.OutboundTransaction, bool, error) {
	chainID, ok := outboundRepoChainIDFromName(request.Chain)
	if !ok {
		return nil, false, fmt.Errorf("%w: unsupported withdrawal chain %q", ErrOutboundTransactionInvalid, request.Chain)
	}
	return r.Create(ctx, OutboundTransactionCreate{
		IdempotencyKey: fmt.Sprintf("%s:%s:primary", models.OutboundResourceWithdrawal, request.ID),
		ResourceType:   models.OutboundResourceWithdrawal,
		ResourceID:     request.ID,
		MerchantID:     request.MerchantID,
		DomainID:       request.DomainID,
		WalletID:       request.WalletID,
		ChainID:        chainID,
		ChainName:      constants.ChainName(chainID),
		Token:          normalizedOptionalString(request.Token),
		Symbol:         request.Symbol,
		Decimals:       request.Decimals,
		AmountRaw:      request.AmountRaw,
		ToAddress:      request.ToAddress,
		ActorID:        actorID,
		CorrelationID:  request.CorrelationID,
	})
}

func (r *OutboundTransactionRepo) CreateForRefund(ctx context.Context, refund models.Refund, actorID string) (*models.OutboundTransaction, bool, error) {
	chainID, ok := outboundRepoChainIDFromName(refund.Chain)
	if !ok {
		return nil, false, fmt.Errorf("%w: unsupported refund chain %q", ErrOutboundTransactionInvalid, refund.Chain)
	}
	if refund.WalletID == nil || *refund.WalletID == uuid.Nil {
		return nil, false, fmt.Errorf("%w: refund wallet id is required", ErrOutboundTransactionInvalid)
	}
	domainID := refund.DomainID
	return r.Create(ctx, OutboundTransactionCreate{
		IdempotencyKey: fmt.Sprintf("%s:%s:primary", models.OutboundResourceRefund, refund.ID),
		ResourceType:   models.OutboundResourceRefund,
		ResourceID:     refund.ID,
		MerchantID:     refund.MerchantID,
		DomainID:       &domainID,
		WalletID:       *refund.WalletID,
		ChainID:        chainID,
		ChainName:      constants.ChainName(chainID),
		Token:          normalizedOptionalString(refund.Token),
		Symbol:         refund.Symbol,
		Decimals:       refund.Decimals,
		AmountRaw:      refund.AmountRaw,
		ToAddress:      refund.ToAddress,
		ActorID:        actorID,
		CorrelationID:  refund.CorrelationID,
	})
}

func (r *OutboundTransactionRepo) CreateForSweepJob(ctx context.Context, job models.SweepJob, txModel models.Transaction, reserveAddress string) (*models.OutboundTransaction, bool, error) {
	return r.Create(ctx, OutboundTransactionCreate{
		IdempotencyKey: fmt.Sprintf("%s:%s:primary", models.OutboundResourceSweepJob, job.ID),
		ResourceType:   models.OutboundResourceSweepJob,
		ResourceID:     job.ID,
		MerchantID:     job.MerchantID,
		WalletID:       job.WalletID,
		ChainID:        job.ChainID,
		ChainName:      constants.ChainName(job.ChainID),
		Token:          normalizedOptionalString(job.Token),
		Symbol:         txModel.Symbol,
		Decimals:       txModel.Decimals,
		AmountRaw:      txModel.Amount,
		ToAddress:      reserveAddress,
		ActorID:        "sweep_worker",
		CorrelationID:  "sweep_job:" + job.ID.String(),
	})
}

func (r *OutboundTransactionRepo) ClaimDue(ctx context.Context, resourceTypes []string, limit int, lockFor time.Duration) ([]models.OutboundTransaction, error) {
	if r == nil || r.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if lockFor <= 0 {
		lockFor = 90 * time.Second
	}
	resourceTypes = normalizeOutboundResourceTypes(resourceTypes)
	if len(resourceTypes) == 0 {
		resourceTypes = []string{models.OutboundResourceWithdrawal, models.OutboundResourceRefund}
	}

	now := time.Now()
	lockUntil := now.Add(lockFor)
	var rows []models.OutboundTransaction
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("resource_type IN ?", resourceTypes).
			// A6: never hand an already-attempted, broadcast or terminal row to
			// the send executor; only clean first attempts are claimable here.
			Where(outboundSendableClaimSQL).
			Where("(next_run_at IS NULL OR next_run_at <= ?)", now).
			Where("(locked_until IS NULL OR locked_until < ?)", now).
			Order("created_at ASC").
			Limit(limit).
			Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return tx.Model(&models.OutboundTransaction{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"locked_until": &lockUntil,
				"updated_at":   now,
			}).Error
	})
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].LockedUntil = &lockUntil
	}
	return rows, nil
}

// ClaimUncertainForRecovery leases rows whose broadcast outcome is unknown
// (status broadcast_attempted, with or without a persisted hash). These rows are
// never handed to the send executor: callers must move them to the operator
// reconciliation path via MarkBroadcastRecoveryRequired. The lease expires like
// any other claim, so a crashed worker cannot strand them.
func (r *OutboundTransactionRepo) ClaimUncertainForRecovery(ctx context.Context, resourceTypes []string, limit int, lockFor time.Duration) ([]models.OutboundTransaction, error) {
	if r == nil || r.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if lockFor <= 0 {
		lockFor = 90 * time.Second
	}
	resourceTypes = normalizeOutboundResourceTypes(resourceTypes)
	if len(resourceTypes) == 0 {
		resourceTypes = []string{models.OutboundResourceWithdrawal, models.OutboundResourceRefund}
	}
	now := time.Now()
	lockUntil := now.Add(lockFor)
	var rows []models.OutboundTransaction
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("resource_type IN ?", resourceTypes).
			Where("status = ?", models.OutboundStatusBroadcastAttempted).
			Where("(locked_until IS NULL OR locked_until < ?)", now).
			Order("created_at ASC").
			Limit(limit).
			Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return tx.Model(&models.OutboundTransaction{}).
			Where("id IN ?", ids).
			Updates(map[string]any{"locked_until": &lockUntil, "updated_at": now}).Error
	})
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].LockedUntil = &lockUntil
	}
	return rows, nil
}

// MarkBroadcastRecoveryRequired turns an uncertain broadcast_attempted row into
// an explicit operator task. The attempt evidence is preserved, so the row stays
// permanently ineligible for an automatic send.
func (r *OutboundTransactionRepo) MarkBroadcastRecoveryRequired(ctx context.Context, id uuid.UUID, detail string) error {
	now := time.Now()
	return r.updateState(ctx, id, []string{models.OutboundStatusBroadcastAttempted}, map[string]any{
		"status":         models.OutboundStatusNeedsOperatorAction,
		"error_category": "broadcast_recovery_required",
		"error_detail":   boundedOutboundText(detail, 1000),
		"locked_until":   nil,
		"next_run_at":    nil,
		"updated_at":     now,
	})
}

// OutboundSendGrant is the atomically acquired, exclusive right to broadcast one
// outbound transaction. Outbound is the persisted row as committed inside the
// grant transaction; Reservation is the chain resource held for exactly that
// attempt. Callers must build the signer request from Outbound only.
type OutboundSendGrant struct {
	Outbound    models.OutboundTransaction
	Reservation models.OutboundChainResourceReservation
}

// OutboundSendBlock explains a refused grant. HardEvidence marks rows or
// resources that already carry hash, attempt, broadcast, finality or terminal
// evidence: those must be resolved through operator reconciliation, not retried.
type OutboundSendBlock struct {
	Reason       string
	HardEvidence bool
	Row          *models.OutboundTransaction
}

// AcquireSendGrant is the single atomic send boundary (A6-R1/R2). Inside one
// short transaction it takes, in this fixed order:
//
//  1. the per-resource advisory lock,
//  2. the linked withdrawal/refund row FOR UPDATE, verified from the caller's
//     prepared resource identity (resource before outbound, exactly like the
//     approval path, so no path can invert the order and nothing waits for a
//     resource while already holding the outbound row),
//  3. this outbound row FOR UPDATE, checked to still belong to that resource,
//  4. the chain-resource advisory lock inside reserveResourceWithDB.
//
// Under those locks it re-verifies that the row is still a clean first attempt,
// that the caller's prepared snapshot still matches the persisted intent, that
// the linked resource is still processing with no hash, broadcast or finality
// evidence, and that no sibling intent of the resource holds broadcast evidence;
// it then reserves the chain resource and records the broadcast attempt in the
// same transaction. If the attempt transition cannot be applied the whole
// transaction rolls back, so a reservation is never committed without it.
//
// Exactly one caller can win: a concurrent worker observes the committed
// broadcast_attempted state (or the occupied chain resource) and gets a block
// with no reservation, so nothing it does can release the winner's resource. No
// external (RPC/signer/network) call happens inside the transaction.
func (r *OutboundTransactionRepo) AcquireSendGrant(ctx context.Context, prepared models.OutboundTransaction, walletAddress string, leaseFor time.Duration) (*OutboundSendGrant, *OutboundSendBlock, error) {
	if r == nil || r.db == nil {
		return nil, nil, gorm.ErrInvalidDB
	}
	prepared.ResourceType = strings.ToLower(strings.TrimSpace(prepared.ResourceType))
	if prepared.ID == uuid.Nil || prepared.ResourceType == "" || prepared.ResourceID == uuid.Nil || prepared.WalletID == uuid.Nil {
		return nil, nil, ErrOutboundTransactionInvalid
	}
	if strings.TrimSpace(walletAddress) == "" {
		return nil, nil, ErrOutboundTransactionInvalid
	}

	var grant *OutboundSendGrant
	var block *OutboundSendBlock
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lockKey := fmt.Sprintf("outbound-resource:%s:%s", prepared.ResourceType, prepared.ResourceID)
		if err := tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtext(?))", lockKey).Error; err != nil {
			return err
		}

		// Resource before outbound: this is the first row lock the grant waits on.
		resourceReason, resourceHard, err := outboundResourceBlockedReason(ctx, tx, prepared.ResourceType, prepared.ResourceID, true)
		if err != nil {
			return err
		}
		if resourceReason != "" {
			block = &OutboundSendBlock{Reason: resourceReason, HardEvidence: resourceHard}
			return nil
		}

		var row models.OutboundTransaction
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", prepared.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				block = &OutboundSendBlock{Reason: "outbound_row_missing", HardEvidence: true}
				return nil
			}
			return err
		}
		snapshot := row
		if strings.ToLower(strings.TrimSpace(row.ResourceType)) != prepared.ResourceType || row.ResourceID != prepared.ResourceID {
			// The row no longer belongs to the resource that was just locked and
			// verified, so this grant must not proceed on either of them.
			block = &OutboundSendBlock{Reason: "resource_identity_drift", HardEvidence: false, Row: &snapshot}
			return nil
		}
		if !outboundClaimableForSend(row) {
			block = &OutboundSendBlock{Reason: outboundBlockedReason(row), HardEvidence: true, Row: &snapshot}
			return nil
		}
		if !outboundIntentMatches(prepared, row) {
			block = &OutboundSendBlock{Reason: "intent_changed_since_precheck", HardEvidence: false, Row: &snapshot}
			return nil
		}
		siblings, err := countOutboundResourceIntents(ctx, tx, row)
		if err != nil {
			return err
		}
		if siblings > 0 {
			block = &OutboundSendBlock{Reason: "conflicting_resource_intent", HardEvidence: true, Row: &snapshot}
			return nil
		}
		reservation, _, err := reserveResourceWithDB(ctx, tx, OutboundResourceReservationRequest{
			OutboundTransactionID: row.ID,
			ResourceType:          models.OutboundResourceReservationSequence,
			ChainID:               row.ChainID,
			ChainName:             row.ChainName,
			WalletID:              row.WalletID,
			WalletAddress:         walletAddress,
			OwnerType:             row.ResourceType,
			OwnerID:               row.ResourceID,
			Intent:                row.ResourceType + ":broadcast",
			LeaseFor:              leaseFor,
		})
		if err != nil {
			if errors.Is(err, ErrOutboundResourceAlreadyReserved) {
				block = &OutboundSendBlock{Reason: "chain_resource_occupied_by_other_intent", HardEvidence: true, Row: &snapshot}
				return nil
			}
			return err
		}

		now := time.Now()
		result := tx.WithContext(ctx).
			Model(&models.OutboundTransaction{}).
			Where("id = ? AND status IN ?", row.ID, []string{models.OutboundStatusPrepared, models.OutboundStatusSigned, models.OutboundStatusFailed}).
			Where("tx_hash = '' AND broadcast_attempted_at IS NULL AND broadcasted_at IS NULL AND finalized_at IS NULL").
			Where(outboundTerminalCategoryGuardSQL).
			Updates(map[string]any{
				"status":                 models.OutboundStatusBroadcastAttempted,
				"attempts":               gorm.Expr("attempts + 1"),
				"broadcast_attempted_at": &now,
				"error_category":         "",
				"error_detail":           "",
				"updated_at":             now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			// A reservation must never be committed without the attempt transition
			// that legitimises it: fail the transaction so the insert rolls back.
			return errSendGrantAttemptTransitionLost
		}
		row.Status = models.OutboundStatusBroadcastAttempted
		row.Attempts++
		row.BroadcastAttemptedAt = &now
		row.ErrorCategory = ""
		row.ErrorDetail = ""
		row.UpdatedAt = now
		grant = &OutboundSendGrant{Outbound: row, Reservation: *reservation}
		return nil
	})
	if err != nil {
		if errors.Is(err, errSendGrantAttemptTransitionLost) {
			return nil, &OutboundSendBlock{Reason: "send_grant_lost_race", HardEvidence: true}, nil
		}
		return nil, nil, err
	}
	return grant, block, nil
}

// AcquireSweepSendGrant is the sweep counterpart of AcquireSendGrant (A10). Sweep
// rows cannot use the withdrawal/refund grant because the sweep job row is the
// authority for "already broadcast". Inside one short transaction it locks, in this
// fixed order:
//
//  1. the per-resource advisory lock,
//  2. the sweep job row FOR UPDATE,
//  3. this outbound row FOR UPDATE (verified to belong to that job),
//  4. the chain-resource advisory lock inside reserveResourceWithDB.
//
// It refuses when the job already carries a broadcast hash, the reconcile marker
// or a terminal/broadcast-uncertain state, and when the outbound (or a sibling
// intent of the same job) already carries attempt/hash/broadcast/finality/terminal
// evidence. That fence is what prevents an expired lease, a failed status or an
// operator retry from re-authorizing an automatic broadcast. No external call
// happens inside the transaction; losers receive no reservation.
func (r *OutboundTransactionRepo) AcquireSweepSendGrant(ctx context.Context, job models.SweepJob, outboundID uuid.UUID, walletAddress string, leaseFor time.Duration) (*OutboundSendGrant, *OutboundSendBlock, error) {
	if r == nil || r.db == nil {
		return nil, nil, gorm.ErrInvalidDB
	}
	if job.ID == uuid.Nil || outboundID == uuid.Nil {
		return nil, nil, ErrOutboundTransactionInvalid
	}
	if strings.TrimSpace(walletAddress) == "" {
		return nil, nil, ErrOutboundTransactionInvalid
	}
	if job.LockedUntil == nil {
		// The caller must present the claim it received from ClaimDue. The persisted
		// status and lease are verified inside the transaction: ClaimDue returns the
		// pre-claim status (so main can still distinguish stale processing recovery)
		// together with the lease this claim took.
		return nil, &OutboundSendBlock{Reason: "sweep_job_not_claimed", HardEvidence: true}, nil
	}

	var grant *OutboundSendGrant
	var block *OutboundSendBlock
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lockKey := fmt.Sprintf("outbound-resource:%s:%s", models.OutboundResourceSweepJob, job.ID)
		if err := tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtext(?))", lockKey).Error; err != nil {
			return err
		}

		// A10-R1 lock order: source transaction → sweep job → outbound → reservation.
		// The source is re-read and re-verified with the existing canonical sweep rule
		// (confirmed + finalized + not reorged); no new finality policy is introduced.
		source, canonical, err := canonicalSweepSourceForRevival(ctx, tx, models.Transaction{UniqueHash: job.TransactionUniqueHash})
		if err != nil {
			return err
		}
		if !canonical {
			block = &OutboundSendBlock{Reason: "sweep_source_not_canonical", HardEvidence: true}
			return nil
		}

		var lockedJob models.SweepJob
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedJob, "id = ?", job.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				block = &OutboundSendBlock{Reason: "sweep_job_missing", HardEvidence: true}
				return nil
			}
			return err
		}
		if reason := sweepJobBroadcastBlockReason(lockedJob); reason != "" {
			block = &OutboundSendBlock{Reason: reason, HardEvidence: true}
			return nil
		}
		if lockedJob.Status != models.SweepJobStatusProcessing || lockedJob.LockedUntil == nil {
			block = &OutboundSendBlock{Reason: "sweep_job_not_claimed", HardEvidence: true}
			return nil
		}
		if !sweepLeaseMatches(job.LockedUntil, lockedJob.LockedUntil) {
			// A different worker claimed (or re-claimed) this job: this caller's claim
			// is stale, and it must never borrow another worker's lease.
			block = &OutboundSendBlock{Reason: "sweep_job_claim_mismatch", HardEvidence: true}
			return nil
		}
		if !time.Now().Before(*lockedJob.LockedUntil) {
			block = &OutboundSendBlock{Reason: "sweep_job_claim_expired", HardEvidence: true}
			return nil
		}
		if source.MerchantID == nil || *source.MerchantID != lockedJob.MerchantID ||
			source.WalletID == nil || *source.WalletID != lockedJob.WalletID ||
			source.ChainID != lockedJob.ChainID || !sameOptionalToken(source.Token, lockedJob.Token) {
			block = &OutboundSendBlock{Reason: "sweep_source_identity_drift", HardEvidence: true}
			return nil
		}

		var row models.OutboundTransaction
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", outboundID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				block = &OutboundSendBlock{Reason: "outbound_row_missing", HardEvidence: true}
				return nil
			}
			return err
		}
		snapshot := row
		if strings.ToLower(strings.TrimSpace(row.ResourceType)) != models.OutboundResourceSweepJob || row.ResourceID != lockedJob.ID {
			block = &OutboundSendBlock{Reason: "resource_identity_drift", HardEvidence: false, Row: &snapshot}
			return nil
		}
		if !outboundClaimableForSend(row) {
			block = &OutboundSendBlock{Reason: outboundBlockedReason(row), HardEvidence: true, Row: &snapshot}
			return nil
		}
		siblings, err := countOutboundResourceIntents(ctx, tx, row)
		if err != nil {
			return err
		}
		if siblings > 0 {
			block = &OutboundSendBlock{Reason: "conflicting_resource_intent", HardEvidence: true, Row: &snapshot}
			return nil
		}
		reservation, _, err := reserveResourceWithDB(ctx, tx, OutboundResourceReservationRequest{
			OutboundTransactionID: row.ID,
			ResourceType:          models.OutboundResourceReservationSequence,
			ChainID:               row.ChainID,
			ChainName:             row.ChainName,
			WalletID:              row.WalletID,
			WalletAddress:         walletAddress,
			OwnerType:             row.ResourceType,
			OwnerID:               row.ResourceID,
			Intent:                row.ResourceType + ":broadcast",
			LeaseFor:              leaseFor,
		})
		if err != nil {
			if errors.Is(err, ErrOutboundResourceAlreadyReserved) {
				block = &OutboundSendBlock{Reason: "chain_resource_occupied_by_other_intent", HardEvidence: true, Row: &snapshot}
				return nil
			}
			return err
		}

		now := time.Now()
		result := tx.WithContext(ctx).
			Model(&models.OutboundTransaction{}).
			Where("id = ? AND status IN ?", row.ID, []string{models.OutboundStatusPrepared, models.OutboundStatusSigned, models.OutboundStatusFailed}).
			Where("tx_hash = '' AND broadcast_attempted_at IS NULL AND broadcasted_at IS NULL AND finalized_at IS NULL").
			Where(outboundTerminalCategoryGuardSQL).
			Updates(map[string]any{
				"status":                 models.OutboundStatusBroadcastAttempted,
				"attempts":               gorm.Expr("attempts + 1"),
				"broadcast_attempted_at": &now,
				"error_category":         "",
				"error_detail":           "",
				"updated_at":             now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			// Roll the whole grant back: a reservation must never be committed
			// without the attempt transition that legitimises it.
			return errSendGrantAttemptTransitionLost
		}
		row.Status = models.OutboundStatusBroadcastAttempted
		row.Attempts++
		row.BroadcastAttemptedAt = &now
		row.ErrorCategory = ""
		row.ErrorDetail = ""
		row.UpdatedAt = now
		grant = &OutboundSendGrant{Outbound: row, Reservation: *reservation}
		return nil
	})
	if err != nil {
		if errors.Is(err, errSendGrantAttemptTransitionLost) {
			return nil, &OutboundSendBlock{Reason: "send_grant_lost_race", HardEvidence: true}, nil
		}
		return nil, nil, err
	}
	return grant, block, nil
}

// sweepJobBroadcastBlockReason reports why a sweep job already carries broadcast
// evidence. A dead_letter status alone is not evidence (a clean pre-broadcast
// failure may be retried after operator recovery); the hash, the reconcile marker,
// a broadcast-uncertain category and a succeeded outcome are.
func sweepJobBroadcastBlockReason(job models.SweepJob) string {
	if strings.TrimSpace(job.SweepTxHash) != "" {
		return "sweep_job_broadcast_recorded"
	}
	if strings.TrimSpace(job.Status) == models.SweepJobStatusSucceeded {
		return "sweep_job_succeeded"
	}
	if strings.TrimSpace(job.OperatorAction) == models.SweepOperatorActionReconcileBroadcast {
		return "sweep_job_reconcile_required"
	}
	if strings.TrimSpace(job.FailureCategory) == models.SweepFailureCategoryBroadcastUncertain {
		return "sweep_job_broadcast_uncertain"
	}
	return ""
}

// sweepLeaseMatches compares a caller's claimed lease against the persisted one.
// PostgreSQL timestamps have microsecond precision, so only that difference is
// tolerated; a different claim instant is a different claim.
func sweepLeaseMatches(claimed, persisted *time.Time) bool {
	if claimed == nil || persisted == nil {
		return false
	}
	return claimed.Truncate(time.Microsecond).Equal(persisted.Truncate(time.Microsecond))
}

// markBroadcastedGuardedWithDB is the transaction-bound form of MarkBroadcasted for
// the sweep broadcast record: the outbound hash must commit together with the sweep
// job's reconcile marker. A finalized row is never rewritten.
func markBroadcastedGuardedWithDB(ctx context.Context, tx *gorm.DB, id uuid.UUID, txHash string) error {
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return ErrTxHashRequired
	}
	now := time.Now()
	result := tx.WithContext(ctx).
		Model(&models.OutboundTransaction{}).
		// Only a row that actually attempted a broadcast can record one, and an
		// existing different hash is never overwritten.
		Where("id = ? AND status IN ?", id, []string{models.OutboundStatusBroadcastAttempted, models.OutboundStatusBroadcasted}).
		Where("broadcast_attempted_at IS NOT NULL").
		Where("tx_hash = '' OR tx_hash = ?", txHash).
		Where("finalized_at IS NULL").
		Updates(map[string]any{
			"status":         models.OutboundStatusBroadcasted,
			"tx_hash":        txHash,
			"broadcasted_at": &now,
			"locked_until":   nil,
			"next_run_at":    nil,
			"error_category": "",
			"error_detail":   "",
			"updated_at":     now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// consumeResourceWithDB consumes exactly the reservation granted for this attempt
// inside the caller's transaction.
func consumeResourceWithDB(ctx context.Context, tx *gorm.DB, reservationID uuid.UUID, txHash string) error {
	now := time.Now()
	result := tx.WithContext(ctx).
		Model(&models.OutboundChainResourceReservation{}).
		Where("id = ? AND status = ?", reservationID, models.OutboundResourceReservationReserved).
		Updates(map[string]any{
			"status":      models.OutboundResourceReservationConsumed,
			"consumed_at": &now,
			"tx_hash":     strings.TrimSpace(txHash),
			"updated_at":  now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// outboundIntentMatches reports whether a caller's prepared snapshot still
// describes the persisted intent. The executor resolves the source wallet and
// validates the transfer from the pre-grant snapshot, so any drift between that
// snapshot and the locked row (wallet, chain, amount, destination, token,
// merchant/domain, audit context) must refuse the grant instead of broadcasting
// a stale intent.
func outboundIntentMatches(prepared, persisted models.OutboundTransaction) bool {
	if prepared.ID != persisted.ID || prepared.ResourceID != persisted.ResourceID {
		return false
	}
	if strings.ToLower(strings.TrimSpace(prepared.ResourceType)) != strings.ToLower(strings.TrimSpace(persisted.ResourceType)) {
		return false
	}
	if prepared.WalletID != persisted.WalletID || prepared.ChainID != persisted.ChainID || prepared.MerchantID != persisted.MerchantID {
		return false
	}
	if !optionalUUIDsEqual(prepared.DomainID, persisted.DomainID) {
		return false
	}
	if strings.TrimSpace(prepared.AmountRaw) != strings.TrimSpace(persisted.AmountRaw) || strings.TrimSpace(prepared.ToAddress) != strings.TrimSpace(persisted.ToAddress) {
		return false
	}
	if !optionalStringsEqual(prepared.Token, persisted.Token) {
		return false
	}
	if strings.TrimSpace(prepared.Symbol) != strings.TrimSpace(persisted.Symbol) || prepared.Decimals != persisted.Decimals {
		return false
	}
	if strings.TrimSpace(prepared.ActorID) != strings.TrimSpace(persisted.ActorID) || strings.TrimSpace(prepared.CorrelationID) != strings.TrimSpace(persisted.CorrelationID) {
		return false
	}
	return true
}

func optionalUUIDsEqual(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func optionalStringsEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return strings.TrimSpace(*a) == strings.TrimSpace(*b)
}

// InspectSendableForExecution is the executor's fast, unlocked pre-check. It
// returns the current persisted row, so the caller never keeps working from a
// stale claimed snapshot, plus the block reason and whether the block rests on
// hard evidence (hash/attempt/broadcast/finality/terminal) that only operator
// reconciliation may resolve. It never authorises a send: AcquireSendGrant
// re-verifies the same invariants under locks.
func (r *OutboundTransactionRepo) InspectSendableForExecution(ctx context.Context, id uuid.UUID) (*models.OutboundTransaction, string, bool, error) {
	if r == nil || r.db == nil {
		return nil, "", false, gorm.ErrInvalidDB
	}
	var row models.OutboundTransaction
	if err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "outbound_row_missing", true, nil
		}
		return nil, "", false, err
	}
	if !outboundClaimableForSend(row) {
		return &row, outboundBlockedReason(row), true, nil
	}
	resourceReason, resourceHard, err := outboundResourceBlockedReason(ctx, r.db, row.ResourceType, row.ResourceID, false)
	if err != nil {
		return nil, "", false, err
	}
	if resourceReason != "" {
		return &row, resourceReason, resourceHard, nil
	}
	conflicts, err := countOutboundResourceIntents(ctx, r.db, row)
	if err != nil {
		return nil, "", false, err
	}
	if conflicts > 0 {
		return &row, "conflicting_resource_intent", true, nil
	}
	return &row, "", false, nil
}

func outboundBlockedReason(row models.OutboundTransaction) string {
	switch {
	case row.Status == models.OutboundStatusBroadcastAttempted:
		return "broadcast_attempted_uncertain"
	case strings.TrimSpace(row.TxHash) != "":
		return "hash_already_persisted"
	case row.BroadcastedAt != nil:
		return "already_broadcasted"
	case outboundTerminalErrorCategory(row.ErrorCategory):
		return "terminal_failed"
	case row.FinalizedAt != nil || row.Status == models.OutboundStatusFinalized:
		return "already_finalized"
	default:
		return "not_sendable_state:" + strings.TrimSpace(row.Status)
	}
}

// outboundResourceBlockedReason inspects the withdrawal/refund identified by the
// caller (normally the prepared resource identity), so a grant can lock and
// verify the resource before it ever touches the outbound row. With lockForUpdate
// it takes that row FOR UPDATE, which is what the grant transaction needs: the
// resource must still be processing and must carry no hash, broadcast or finality
// evidence at the moment the send right is granted.
//
// The second return value marks resource states that are already broadcast or
// final: those are hard evidence, so callers must leave the outbound row (and any
// broadcast evidence it holds) untouched instead of overwriting it with an
// operator task.
func outboundResourceBlockedReason(ctx context.Context, db *gorm.DB, resourceType string, resourceID uuid.UUID, lockForUpdate bool) (string, bool, error) {
	query := db.WithContext(ctx)
	if lockForUpdate {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	switch strings.ToLower(strings.TrimSpace(resourceType)) {
	case models.OutboundResourceWithdrawal:
		var request models.WithdrawalRequest
		if err := query.Select("status", "tx_hash", "broadcasted_at", "finalized_at").First(&request, "id = ?", resourceID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "resource_missing", false, nil
			}
			return "", false, err
		}
		if request.Status == models.WithdrawalStatusFinalized || request.FinalizedAt != nil {
			return "resource_already_finalized", true, nil
		}
		if strings.TrimSpace(request.TxHash) != "" || request.BroadcastedAt != nil {
			return "resource_already_broadcast", true, nil
		}
		if request.Status != models.WithdrawalStatusProcessing {
			return "resource_not_processing", false, nil
		}
	case models.OutboundResourceRefund:
		var refund models.Refund
		if err := query.Select("status", "tx_hash", "broadcasted_at", "finalized_at").First(&refund, "id = ?", resourceID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "resource_missing", false, nil
			}
			return "", false, err
		}
		if refund.Status == models.RefundStatusSucceeded || refund.FinalizedAt != nil {
			return "resource_already_finalized", true, nil
		}
		if strings.TrimSpace(refund.TxHash) != "" || refund.BroadcastedAt != nil {
			return "resource_already_broadcast", true, nil
		}
		if refund.Status != models.RefundStatusProcessing {
			return "resource_not_processing", false, nil
		}
	}
	return "", false, nil
}

func countOutboundResourceIntents(ctx context.Context, db *gorm.DB, row models.OutboundTransaction) (int64, error) {
	var conflicts int64
	err := db.WithContext(ctx).
		Model(&models.OutboundTransaction{}).
		Where("resource_type = ? AND resource_id = ? AND id <> ?", row.ResourceType, row.ResourceID, row.ID).
		Where(outboundBlockingEvidenceSQL).
		Count(&conflicts).Error
	return conflicts, err
}

func (r *OutboundTransactionRepo) MarkBroadcastAttempted(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	return r.updateStateWhere(ctx, id, []string{models.OutboundStatusPrepared, models.OutboundStatusSigned, models.OutboundStatusFailed},
		"tx_hash = '' AND broadcast_attempted_at IS NULL AND broadcasted_at IS NULL AND finalized_at IS NULL AND "+outboundTerminalCategoryGuardSQL, nil, map[string]any{
			"status":                 models.OutboundStatusBroadcastAttempted,
			"attempts":               gorm.Expr("attempts + 1"),
			"broadcast_attempted_at": &now,
			"error_category":         "",
			"error_detail":           "",
			"updated_at":             now,
		})
}

// RecordObservedBroadcastHash binds an observed transaction hash to the exact
// broadcast attempt that produced it. It is the only way a hash can be recorded
// for a row that is no longer a sendable first attempt (for example after the
// recovery loop moved an uncertain attempt to the operator queue): the caller
// must present the attempt timestamp it was granted under, the write only applies
// while that attempt is still the persisted one and no hash is stored yet, and it
// grants no new send right (the status is left untouched).
func (r *OutboundTransactionRepo) RecordObservedBroadcastHash(ctx context.Context, id uuid.UUID, attemptAt *time.Time, txHash string) error {
	if attemptAt == nil || attemptAt.IsZero() {
		return fmt.Errorf("%w: broadcast attempt timestamp is required", ErrOutboundTransactionInvalid)
	}
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return ErrTxHashRequired
	}
	now := time.Now()
	return r.updateStateWhere(ctx, id,
		[]string{models.OutboundStatusPrepared, models.OutboundStatusSigned, models.OutboundStatusBroadcastAttempted, models.OutboundStatusBroadcasted, models.OutboundStatusConfirming, models.OutboundStatusFailed, models.OutboundStatusNeedsOperatorAction},
		"tx_hash = '' AND broadcast_attempted_at = ?", []any{*attemptAt}, map[string]any{
			"tx_hash":    txHash,
			"updated_at": now,
		})
}

func (r *OutboundTransactionRepo) MarkBroadcasted(ctx context.Context, id uuid.UUID, txHash string) error {
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return ErrTxHashRequired
	}
	now := time.Now()
	return r.updateStateWhere(ctx, id, []string{models.OutboundStatusPrepared, models.OutboundStatusSigned, models.OutboundStatusBroadcastAttempted, models.OutboundStatusBroadcasted}, "finalized_at IS NULL", nil, map[string]any{
		"status":         models.OutboundStatusBroadcasted,
		"tx_hash":        txHash,
		"broadcasted_at": &now,
		"locked_until":   nil,
		"next_run_at":    nil,
		"error_category": "",
		"error_detail":   "",
		"updated_at":     now,
	})
}

func (r *OutboundTransactionRepo) MarkNeedsOperatorAction(ctx context.Context, id uuid.UUID, category string, detail string) error {
	now := time.Now()
	return r.updateStateWhere(ctx, id,
		[]string{models.OutboundStatusPrepared, models.OutboundStatusSigned, models.OutboundStatusBroadcastAttempted, models.OutboundStatusBroadcasted, models.OutboundStatusConfirming, models.OutboundStatusFailed, models.OutboundStatusNeedsOperatorAction},
		"finalized_at IS NULL", nil, map[string]any{
			"status": models.OutboundStatusNeedsOperatorAction,
			// Terminal broadcast evidence is never erased: a row that already
			// carries a terminal category keeps it, so the operator still sees the
			// original evidence next to the new operator task.
			"error_category": gorm.Expr("CASE WHEN "+outboundTerminalCategoryGuardSQL+" THEN ? ELSE error_category END", boundedOutboundText(category, 80)),
			"error_detail":   boundedOutboundText(detail, 1000),
			"locked_until":   nil,
			"next_run_at":    nil,
			"updated_at":     now,
		})
}

func (r *OutboundTransactionRepo) MarkFailed(ctx context.Context, id uuid.UUID, err error, retryAfter time.Duration) error {
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	now := time.Now()
	var nextRunAt *time.Time
	status := models.OutboundStatusFailed
	if retryAfter > 0 {
		next := now.Add(retryAfter)
		nextRunAt = &next
	} else {
		status = models.OutboundStatusNeedsOperatorAction
	}
	return r.updateStateWhere(ctx, id,
		[]string{models.OutboundStatusPrepared, models.OutboundStatusSigned, models.OutboundStatusBroadcastAttempted, models.OutboundStatusFailed},
		"finalized_at IS NULL AND "+outboundTerminalCategoryGuardSQL, nil, map[string]any{
			"status":         status,
			"error_category": outboundErrorCategory(err),
			"error_detail":   boundedOutboundText(detail, 1000),
			"locked_until":   nil,
			"next_run_at":    nextRunAt,
			"updated_at":     now,
		})
}

// DeferForNetworkState releases a claimed transaction without consuming a
// broadcast attempt. It is used while an administrator has outbound activity
// disabled for the transaction's network.
func (r *OutboundTransactionRepo) DeferForNetworkState(ctx context.Context, id uuid.UUID, detail string, retryAfter time.Duration) error {
	if retryAfter <= 0 {
		retryAfter = 30 * time.Second
	}
	now := time.Now()
	next := now.Add(retryAfter)
	return r.updateStateWhere(ctx, id, []string{models.OutboundStatusPrepared, models.OutboundStatusSigned, models.OutboundStatusFailed}, "finalized_at IS NULL AND "+outboundTerminalCategoryGuardSQL, nil, map[string]any{
		"error_category": "network_maintenance",
		"error_detail":   boundedOutboundText(detail, 1000),
		"locked_until":   nil,
		"next_run_at":    &next,
		"updated_at":     now,
	})
}

func (r *OutboundTransactionRepo) MarkFinalized(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	return r.updateState(ctx, id, []string{models.OutboundStatusBroadcasted, models.OutboundStatusConfirming}, map[string]any{
		"status":       models.OutboundStatusFinalized,
		"finalized_at": &now,
		"updated_at":   now,
	})
}

func (r *OutboundTransactionRepo) MarkResourceFinalized(ctx context.Context, resourceType string, resourceID uuid.UUID) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	now := time.Now()
	return r.updateResourceState(ctx, resourceType, resourceID, []string{models.OutboundStatusBroadcasted, models.OutboundStatusConfirming, models.OutboundStatusFinalized}, map[string]any{
		"status":         models.OutboundStatusFinalized,
		"finalized_at":   &now,
		"locked_until":   nil,
		"next_run_at":    nil,
		"error_category": "",
		"error_detail":   "",
		"updated_at":     now,
	})
}

func (r *OutboundTransactionRepo) MarkResourceTerminalFailed(ctx context.Context, resourceType string, resourceID uuid.UUID, err error) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	now := time.Now()
	return r.updateResourceState(ctx, resourceType, resourceID, []string{models.OutboundStatusBroadcasted, models.OutboundStatusConfirming, models.OutboundStatusFailed}, map[string]any{
		"status":         models.OutboundStatusFailed,
		"finalized_at":   &now,
		"locked_until":   nil,
		"next_run_at":    nil,
		"error_category": "terminal_failed",
		"error_detail":   boundedOutboundText(detail, 1000),
		"updated_at":     now,
	})
}

func (r *OutboundTransactionRepo) CreateReplacementIntent(ctx context.Context, parentID uuid.UUID, reason string) (*models.OutboundTransaction, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	var parent models.OutboundTransaction
	if err := r.db.WithContext(ctx).First(&parent, "id = ?", parentID).Error; err != nil {
		return nil, false, err
	}
	replacementID := uuid.New()
	params := OutboundTransactionCreate{
		IdempotencyKey: fmt.Sprintf("%s:%s:replacement:%s", parent.ResourceType, parent.ResourceID, replacementID),
		ResourceType:   parent.ResourceType,
		ResourceID:     parent.ResourceID,
		MerchantID:     parent.MerchantID,
		DomainID:       parent.DomainID,
		WalletID:       parent.WalletID,
		ChainID:        parent.ChainID,
		ChainName:      parent.ChainName,
		Token:          parent.Token,
		Symbol:         parent.Symbol,
		Decimals:       parent.Decimals,
		AmountRaw:      parent.AmountRaw,
		ToAddress:      parent.ToAddress,
		ActorID:        parent.ActorID,
		CorrelationID:  parent.CorrelationID,
	}
	child, created, err := r.Create(ctx, params)
	if err != nil {
		return nil, false, err
	}
	updates := map[string]any{
		"status":                models.OutboundStatusNeedsOperatorAction,
		"replacement_parent_id": &parent.ID,
		"replacement_reason":    boundedOutboundText(reason, 160),
		"replaces_tx_hash":      parent.TxHash,
		"error_category":        "replacement_requires_operator",
		"error_detail":          "chain replacement broadcast is not automated by the current chain interface",
	}
	if err := r.db.WithContext(ctx).Model(&models.OutboundTransaction{}).Where("id = ?", child.ID).Updates(updates).Error; err != nil {
		return nil, false, err
	}
	if err := r.db.WithContext(ctx).Model(&models.OutboundTransaction{}).Where("id = ?", parent.ID).Update("status", models.OutboundStatusReplaced).Error; err != nil {
		return nil, false, err
	}
	if err := r.db.WithContext(ctx).First(child, "id = ?", child.ID).Error; err != nil {
		return nil, false, err
	}
	return child, created, nil
}

func (r *OutboundTransactionRepo) updateState(ctx context.Context, id uuid.UUID, allowed []string, updates map[string]any) error {
	return r.updateStateWhere(ctx, id, allowed, "", nil, updates)
}

// updateStateWhere is updateState with an additional raw predicate. Lifecycle
// writes use it to refuse overwriting a row that already carries finality
// evidence, so late/duplicate callbacks can never resurrect a settled outbound.
func (r *OutboundTransactionRepo) updateStateWhere(ctx context.Context, id uuid.UUID, allowed []string, extraWhere string, extraArgs []any, updates map[string]any) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	q := r.db.WithContext(ctx).Model(&models.OutboundTransaction{}).Where("id = ?", id)
	if len(allowed) > 0 {
		q = q.Where("status IN ?", allowed)
	}
	if strings.TrimSpace(extraWhere) != "" {
		q = q.Where(extraWhere, extraArgs...)
	}
	result := q.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *OutboundTransactionRepo) updateResourceState(ctx context.Context, resourceType string, resourceID uuid.UUID, allowed []string, updates map[string]any) error {
	resourceType = strings.ToLower(strings.TrimSpace(resourceType))
	if resourceType == "" || resourceID == uuid.Nil {
		return ErrOutboundTransactionInvalid
	}
	q := r.db.WithContext(ctx).
		Model(&models.OutboundTransaction{}).
		Where("resource_type = ? AND resource_id = ?", resourceType, resourceID)
	if len(allowed) > 0 {
		q = q.Where("status IN ?", allowed)
	}
	result := q.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

type OutboundResourceReservationRequest struct {
	OutboundTransactionID uuid.UUID
	ResourceType          string
	ChainID               constants.ChainID
	ChainName             string
	WalletID              uuid.UUID
	WalletAddress         string
	OwnerType             string
	OwnerID               uuid.UUID
	Intent                string
	Nonce                 *uint64
	UTXOTxID              string
	UTXOVout              *uint32
	UTXOValueRaw          string
	LeaseFor              time.Duration
}

func (r *OutboundTransactionRepo) ReserveSequence(ctx context.Context, outbound models.OutboundTransaction, walletAddress string, leaseFor time.Duration) (*models.OutboundChainResourceReservation, bool, error) {
	return r.ReserveResource(ctx, OutboundResourceReservationRequest{
		OutboundTransactionID: outbound.ID,
		ResourceType:          models.OutboundResourceReservationSequence,
		ChainID:               outbound.ChainID,
		ChainName:             outbound.ChainName,
		WalletID:              outbound.WalletID,
		WalletAddress:         walletAddress,
		OwnerType:             outbound.ResourceType,
		OwnerID:               outbound.ResourceID,
		Intent:                outbound.ResourceType + ":broadcast",
		LeaseFor:              leaseFor,
	})
}

func (r *OutboundTransactionRepo) ReserveResource(ctx context.Context, req OutboundResourceReservationRequest) (*models.OutboundChainResourceReservation, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	var out *models.OutboundChainResourceReservation
	created := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		out, created, err = reserveResourceWithDB(ctx, tx, req)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return out, created, nil
}

// reserveResourceWithDB performs the reservation inside the caller's transaction,
// so the advisory lock, the occupancy check and the insert commit (or roll back)
// together with the caller's other state changes.
func reserveResourceWithDB(ctx context.Context, db *gorm.DB, req OutboundResourceReservationRequest) (*models.OutboundChainResourceReservation, bool, error) {
	prepared, err := prepareOutboundResourceReservation(req)
	if err != nil {
		return nil, false, err
	}
	if err := db.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtext(?))", prepared.ResourceKey).Error; err != nil {
		return nil, false, err
	}
	var existing models.OutboundChainResourceReservation
	err = db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("resource_key = ? AND status IN ?", prepared.ResourceKey, blockingReservationStatuses(prepared.ResourceType)).
		Order("created_at ASC").
		First(&existing).Error
	if err == nil {
		if existing.OutboundTransactionID == prepared.OutboundTransactionID {
			return &existing, false, nil
		}
		return nil, false, ErrOutboundResourceAlreadyReserved
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	if err := db.WithContext(ctx).Create(&prepared).Error; err != nil {
		return nil, false, err
	}
	return &prepared, true, nil
}

func (r *OutboundTransactionRepo) ConsumeResource(ctx context.Context, reservationID uuid.UUID, txHash string) error {
	txHash = strings.TrimSpace(txHash)
	now := time.Now()
	return r.db.WithContext(ctx).Model(&models.OutboundChainResourceReservation{}).
		Where("id = ? AND status = ?", reservationID, models.OutboundResourceReservationReserved).
		Updates(map[string]any{
			"status":      models.OutboundResourceReservationConsumed,
			"consumed_at": &now,
			"tx_hash":     txHash,
			"updated_at":  now,
		}).Error
}

func (r *OutboundTransactionRepo) ReleaseResource(ctx context.Context, reservationID uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&models.OutboundChainResourceReservation{}).
		Where("id = ? AND status = ?", reservationID, models.OutboundResourceReservationReserved).
		Updates(map[string]any{
			"status":      models.OutboundResourceReservationReleased,
			"released_at": &now,
			"updated_at":  now,
		}).Error
}

func prepareOutboundTransaction(params OutboundTransactionCreate) (models.OutboundTransaction, error) {
	resourceType := strings.ToLower(strings.TrimSpace(params.ResourceType))
	if resourceType == "" || params.ResourceID == uuid.Nil || params.MerchantID == uuid.Nil || params.WalletID == uuid.Nil {
		return models.OutboundTransaction{}, fmt.Errorf("%w: resource, merchant and wallet are required", ErrOutboundTransactionInvalid)
	}
	if !constants.IsSupportedChainID(params.ChainID) {
		return models.OutboundTransaction{}, fmt.Errorf("%w: supported chain is required", ErrOutboundTransactionInvalid)
	}
	amountRaw := strings.TrimSpace(params.AmountRaw)
	toAddress := strings.TrimSpace(params.ToAddress)
	if amountRaw == "" || toAddress == "" {
		return models.OutboundTransaction{}, fmt.Errorf("%w: amount and destination are required", ErrOutboundTransactionInvalid)
	}
	key := strings.TrimSpace(params.IdempotencyKey)
	if key == "" {
		key = fmt.Sprintf("%s:%s:primary", resourceType, params.ResourceID)
	}
	chainName := strings.TrimSpace(params.ChainName)
	if chainName == "" {
		chainName = constants.ChainName(params.ChainID)
	}
	maxAttempts := params.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 5
	}
	return models.OutboundTransaction{
		ID:             uuid.New(),
		IdempotencyKey: key,
		ResourceType:   resourceType,
		ResourceID:     params.ResourceID,
		MerchantID:     params.MerchantID,
		DomainID:       params.DomainID,
		WalletID:       params.WalletID,
		ChainID:        params.ChainID,
		ChainName:      chainName,
		Token:          normalizedOptionalString(params.Token),
		Symbol:         strings.ToUpper(strings.TrimSpace(params.Symbol)),
		Decimals:       params.Decimals,
		AmountRaw:      amountRaw,
		ToAddress:      toAddress,
		Status:         models.OutboundStatusPrepared,
		MaxAttempts:    maxAttempts,
		NextRunAt:      params.NextRunAt,
		FeePolicyJSON:  strings.TrimSpace(params.FeePolicyJSON),
		ActorID:        strings.TrimSpace(params.ActorID),
		CorrelationID:  strings.TrimSpace(params.CorrelationID),
	}, nil
}

func prepareOutboundResourceReservation(req OutboundResourceReservationRequest) (models.OutboundChainResourceReservation, error) {
	resourceType := strings.ToLower(strings.TrimSpace(req.ResourceType))
	if req.OutboundTransactionID == uuid.Nil || resourceType == "" || req.OwnerID == uuid.Nil || req.WalletID == uuid.Nil {
		return models.OutboundChainResourceReservation{}, fmt.Errorf("%w: outbound, resource owner and wallet are required", ErrOutboundTransactionInvalid)
	}
	if !constants.IsSupportedChainID(req.ChainID) {
		return models.OutboundChainResourceReservation{}, fmt.Errorf("%w: supported chain is required", ErrOutboundTransactionInvalid)
	}
	walletAddress := strings.TrimSpace(req.WalletAddress)
	if walletAddress == "" {
		return models.OutboundChainResourceReservation{}, fmt.Errorf("%w: wallet address is required", ErrOutboundTransactionInvalid)
	}
	key, err := outboundResourceKey(req)
	if err != nil {
		return models.OutboundChainResourceReservation{}, err
	}
	var leaseExpiresAt *time.Time
	if req.LeaseFor > 0 {
		expires := time.Now().Add(req.LeaseFor)
		leaseExpiresAt = &expires
	}
	chainName := strings.TrimSpace(req.ChainName)
	if chainName == "" {
		chainName = constants.ChainName(req.ChainID)
	}
	return models.OutboundChainResourceReservation{
		ID:                    uuid.New(),
		OutboundTransactionID: req.OutboundTransactionID,
		ResourceType:          resourceType,
		ResourceKey:           key,
		Status:                models.OutboundResourceReservationReserved,
		ChainID:               req.ChainID,
		ChainName:             chainName,
		WalletID:              req.WalletID,
		WalletAddress:         walletAddress,
		OwnerType:             strings.ToLower(strings.TrimSpace(req.OwnerType)),
		OwnerID:               req.OwnerID,
		Intent:                strings.TrimSpace(req.Intent),
		Nonce:                 req.Nonce,
		UTXOTxID:              strings.TrimSpace(req.UTXOTxID),
		UTXOVout:              req.UTXOVout,
		UTXOValueRaw:          strings.TrimSpace(req.UTXOValueRaw),
		LeaseExpiresAt:        leaseExpiresAt,
	}, nil
}

func outboundResourceKey(req OutboundResourceReservationRequest) (string, error) {
	chain := constants.ChainName(req.ChainID)
	wallet := strings.ToLower(strings.TrimSpace(req.WalletAddress))
	switch strings.ToLower(strings.TrimSpace(req.ResourceType)) {
	case models.OutboundResourceReservationSequence:
		return fmt.Sprintf("%s:%s:sequence", chain, wallet), nil
	case models.OutboundResourceReservationNonce:
		if req.Nonce == nil {
			return "", fmt.Errorf("%w: nonce is required", ErrOutboundTransactionInvalid)
		}
		return fmt.Sprintf("%s:%s:nonce:%d", chain, wallet, *req.Nonce), nil
	case models.OutboundResourceReservationUTXO:
		txID := strings.ToLower(strings.TrimSpace(req.UTXOTxID))
		if txID == "" || req.UTXOVout == nil {
			return "", fmt.Errorf("%w: utxo txid and vout are required", ErrOutboundTransactionInvalid)
		}
		return fmt.Sprintf("%s:utxo:%s:%d", chain, txID, *req.UTXOVout), nil
	default:
		return "", fmt.Errorf("%w: unsupported resource type", ErrOutboundTransactionInvalid)
	}
}

func blockingReservationStatuses(resourceType string) []string {
	switch strings.ToLower(strings.TrimSpace(resourceType)) {
	case models.OutboundResourceReservationSequence:
		return []string{models.OutboundResourceReservationReserved}
	default:
		return []string{models.OutboundResourceReservationReserved, models.OutboundResourceReservationConsumed}
	}
}

func outboundRepoChainIDFromName(name string) (constants.ChainID, bool) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch normalized {
	case "binance", "bsc", "bnb":
		normalized = constants.ChainName(constants.Binance)
	case "nile", "tron-nile", "trx-nile", "tron-shasta", "shasta":
		normalized = constants.ChainName(constants.TRONTestnet)
	}
	for _, chainID := range constants.AllChainIDs() {
		if normalized == strings.ToLower(constants.ChainName(chainID)) {
			return chainID, true
		}
	}
	return 0, false
}

func normalizedOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func normalizeOutboundResourceTypes(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func outboundErrorCategory(err error) string {
	if err == nil {
		return ""
	}
	if OutboundTransferFailureBroadcastUncertain(err) {
		return "broadcast_uncertain"
	}
	return "broadcast_failed"
}

func boundedOutboundText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit > 0 && len(value) > limit {
		return value[:limit]
	}
	return value
}
