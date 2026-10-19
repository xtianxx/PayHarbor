// routes/swagger_helpers.go
package routes

import (
	"core/api/router"
	"core/constants"
	"fmt"

	fiber "github.com/gofiber/fiber/v3"
)

// GenerateFakeActionRoutesSwagger adds the action router as fake endpoints for the Swagger UI.
func GenerateFakeActionRoutesSwagger(app *fiber.App, ar *router.ActionRouter) {
	for _, cmd := range constants.AllCommands {
		// Register both GET and POST for Swagger.
		path := fmt.Sprintf("/%s", cmd)

		app.Get(path, func(c fiber.Ctx) error {

			return c.SendString(fmt.Sprintf("Use /packet?action=%s with POST", cmd))
		})
		app.Post(path, func(c fiber.Ctx) error {
			return c.SendString(fmt.Sprintf("Use /packet?action=%s with POST", cmd))
		})
	}
}
