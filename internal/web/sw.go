package web

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"

	"github.com/gofiber/fiber/v2"
)

//go:embed sw.js.tmpl
var swJSSource string

// swJSTemplate renders sw.js.tmpl with the build version injected into the
// cache name. Parsed once at package init since the template source is a
// compile-time constant.
var swJSTemplate = template.Must(template.New("sw.js").Parse(swJSSource))

// handleServiceWorker serves the PWA service worker at the origin root (not
// under /static) so its registration scope covers the whole app. The running
// version is baked into the response body, so the bytes change on every
// release: the browser detects the new script, installs it, and activate()
// purges the previous version's cache — no separate polling needed.
func (s *Server) handleServiceWorker(c *fiber.Ctx) error {
	version := s.upd.Info(c.Context()).Current
	if version == "" {
		version = "dev"
	}

	var buf bytes.Buffer
	if err := swJSTemplate.Execute(&buf, struct{ Version string }{Version: version}); err != nil {
		return fmt.Errorf("sw.js: render: %w", err)
	}

	c.Set("Service-Worker-Allowed", "/")
	c.Set(fiber.HeaderContentType, "application/javascript")
	// The worker body carries the build version, so a new release must reach the
	// browser promptly. Browsers already bypass the HTTP cache for the SW script
	// at least every 24h; no-cache makes the fast-rollout intent explicit and
	// removes any ambiguity from intermediary caches.
	c.Set(fiber.HeaderCacheControl, "no-cache")
	return c.Send(buf.Bytes())
}
