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
	
	// Serve the Swagger UI HTML page at /api/docs
	app.Get("/api/docs", func(c *fiber.Ctx) error {
		return c.SendFile("./api/swagger-ui.html")
	})
	
	// Redirect /api/docs/ to /api/docs
	app.Get("/api/docs/", func(c *fiber.Ctx) error {
		return c.Redirect("/api/docs")
	})
	
	// Legacy redirects for backwards compatibility
	app.Get("/swagger", func(c *fiber.Ctx) error {
		return c.Redirect("/api/docs")
	})
	
	app.Get("/swagger/", func(c *fiber.Ctx) error {
		return c.Redirect("/api/docs")
	})
	
	app.Get("/docs", func(c *fiber.Ctx) error {
		return c.Redirect("/api/docs")
	})
}