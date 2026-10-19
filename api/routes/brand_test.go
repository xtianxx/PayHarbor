package routes

// HTTP product-identifier test for the PayHarbor rename: the real router built
// from the current source must advertise the new brand in the Server header.
// The gorm handle is never dialled (DisableAutomaticPing, unbound DSN), so no
// database or network access happens; template rendering is covered separately
// by the root package's brand tests where ./views is available.

import (
	"net/http/httptest"
	"testing"

	"core/constants"

	"github.com/gofiber/fiber/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestRouterServesPayHarborProductIdentifiers(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "postgres://fixture:fixture@127.0.0.1:1/gateway_brand_fixture?sslmode=disable&connect_timeout=1",
	}), &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("fixture gorm open: %v", err)
	}
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}

	router := NewRouter(db)
	app := router.GetFiber()

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("router app.Test: %v", err)
	}
	if got := resp.Header.Get("Server"); got != "PayHarbor" {
		t.Fatalf("Server header = %q, want PayHarbor", got)
	}
	if constants.PRODUCT_NAME != resp.Header.Get("Server") {
		t.Fatalf("Server header %q does not match constants.PRODUCT_NAME %q", resp.Header.Get("Server"), constants.PRODUCT_NAME)
	}
}
