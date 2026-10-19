package repositories

import (
	"context"
	"errors"
	"testing"

	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestProductRepoUpdatePreservesTokenAndNormalizesDonation(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Product{}); err != nil {
		t.Fatalf("automigrate products: %v", err)
	}

	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	merchant := models.Merchant{ID: merchantID, Name: "Merchant", Email: "merchant@example.com", Password: "x", IsActive: true}
	domain := models.Domain{ID: domainID, MerchantID: merchantID, DomainURL: "merchant.example.com", APIKey: "key", APISecret: "secret", HDAccountID: 1}
	if err := db.WithContext(ctx).Create(&merchant).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if err := db.WithContext(ctx).Create(&domain).Error; err != nil {
		t.Fatalf("seed domain: %v", err)
	}

	repo := NewProductRepo(db)
	product := &models.Product{
		MerchantID: merchantID,
		DomainID:   domainID,
		Name:       "Support",
		LinkType:   models.PaymentLinkTypeFixed,
		Amount:     "10.00",
		Currency:   "USD",
		Language:   "tr",
		SuccessURL: "https://example.com/success",
		CancelURL:  "https://example.com/cancel",
		LogoURL:    "https://example.com/logo.png",
		IsActive:   true,
	}
	if err := repo.Create(ctx, product); err != nil {
		t.Fatalf("create product: %v", err)
	}
	token := product.LinkToken

	product.Name = "Donation"
	product.LinkType = models.PaymentLinkTypeDonation
	product.Amount = "0"
	product.Currency = ""
	product.Language = ""
	product.SuccessURL = "https://example.com/thanks"
	product.CancelURL = ""
	product.LogoURL = ""
	if err := repo.Update(ctx, product); err != nil {
		t.Fatalf("update product: %v", err)
	}

	updated, err := repo.FindByID(ctx, product.ID)
	if err != nil {
		t.Fatalf("find updated product: %v", err)
	}
	if updated.LinkToken != token {
		t.Fatalf("link token changed: %q -> %q", token, updated.LinkToken)
	}
	if updated.LinkType != models.PaymentLinkTypeDonation || updated.Amount != "0" || updated.Currency != "" || updated.Language != models.DefaultProductLanguage {
		t.Fatalf("updated donation product = %#v", updated)
	}
	if updated.SuccessURL != "https://example.com/thanks" || updated.CancelURL != "" || updated.LogoURL != "" {
		t.Fatalf("updated URLs = %#v", updated)
	}

	// An explicitly recorded language must survive updates untouched.
	product.Name = "Support EN"
	product.Language = "en"
	if err := repo.Update(ctx, product); err != nil {
		t.Fatalf("update product with explicit language: %v", err)
	}
	updated, err = repo.FindByID(ctx, product.ID)
	if err != nil {
		t.Fatalf("find explicit-language product: %v", err)
	}
	if updated.Language != "en" {
		t.Fatalf("explicit language not preserved: %q", updated.Language)
	}
	if updated.LinkToken != token {
		t.Fatalf("link token changed after explicit-language update: %q -> %q", token, updated.LinkToken)
	}

	// Historical rows that still hold the retired default keep it when an
	// unrelated field is updated and Language is not cleared. Historical data is
	// never rewritten in bulk; display-time fallback handles it.
	legacy := &models.Product{
		MerchantID: merchantID,
		DomainID:   domainID,
		Name:       "Legacy product",
		LinkType:   models.PaymentLinkTypeFixed,
		Amount:     "7.00",
		Currency:   "USD",
		Language:   "tr",
		IsActive:   true,
	}
	if err := repo.Create(ctx, legacy); err != nil {
		t.Fatalf("create legacy product: %v", err)
	}
	legacy.Name = "Legacy product renamed"
	if err := repo.Update(ctx, legacy); err != nil {
		t.Fatalf("update legacy product: %v", err)
	}
	reloadedLegacy, err := repo.FindByID(ctx, legacy.ID)
	if err != nil {
		t.Fatalf("find legacy product: %v", err)
	}
	if reloadedLegacy.Language != "tr" {
		t.Fatalf("legacy language must be preserved on unrelated update: %q", reloadedLegacy.Language)
	}
	if reloadedLegacy.LinkToken != legacy.LinkToken || reloadedLegacy.Amount != "7.00" || reloadedLegacy.Currency != "USD" {
		t.Fatalf("legacy fields changed on unrelated update: %#v", reloadedLegacy)
	}
}

func TestProductRepoUpdateRequiresSameMerchant(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Product{}); err != nil {
		t.Fatalf("automigrate products: %v", err)
	}

	ctx := context.Background()
	merchantID := uuid.New()
	otherMerchantID := uuid.New()
	domainID := uuid.New()
	if err := db.WithContext(ctx).Create(&models.Merchant{ID: merchantID, Name: "Merchant", Email: "merchant@example.com", Password: "x", IsActive: true}).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if err := db.WithContext(ctx).Create(&models.Merchant{ID: otherMerchantID, Name: "Other", Email: "other@example.com", Password: "x", IsActive: true}).Error; err != nil {
		t.Fatalf("seed other merchant: %v", err)
	}
	if err := db.WithContext(ctx).Create(&models.Domain{ID: domainID, MerchantID: merchantID, DomainURL: "merchant.example.com", APIKey: "key", APISecret: "secret", HDAccountID: 1}).Error; err != nil {
		t.Fatalf("seed domain: %v", err)
	}

	repo := NewProductRepo(db)
	product := &models.Product{
		MerchantID: merchantID,
		DomainID:   domainID,
		Name:       "Fixed",
		LinkType:   models.PaymentLinkTypeFixed,
		Amount:     "10.00",
		Currency:   "USD",
		Language:   "tr",
		IsActive:   true,
	}
	if err := repo.Create(ctx, product); err != nil {
		t.Fatalf("create product: %v", err)
	}

	product.MerchantID = otherMerchantID
	if err := repo.Update(ctx, product); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-merchant update error = %v, want gorm.ErrRecordNotFound", err)
	}
}
