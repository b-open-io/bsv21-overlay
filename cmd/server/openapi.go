package main

import (
	"github.com/gofiber/fiber/v2"
)

// setupOpenAPIDocumentation configures the OpenAPI documentation endpoints
func setupOpenAPIDocumentation(app *fiber.App) {
	// Serve the OpenAPI spec
	app.Get("/api/openapi.yaml", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "application/x-yaml")
		return c.SendFile("./api/openapi.yaml")
	})
	
	// Serve the Swagger UI HTML page
	app.Get("/swagger", func(c *fiber.Ctx) error {
		return c.SendFile("./api/swagger-ui.html")
	})
	
	// Redirect /swagger/ to /swagger
	app.Get("/swagger/", func(c *fiber.Ctx) error {
		return c.Redirect("/swagger")
	})
	
	// Serve the OpenAPI spec at /docs as well for compatibility
	app.Get("/docs", func(c *fiber.Ctx) error {
		return c.SendFile("./api/swagger-ui.html")
	})
}