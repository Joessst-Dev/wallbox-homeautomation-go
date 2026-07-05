package web

import (
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/gofiber/fiber/v2"
)

// setupVM is the view model for the /setup page.
type setupVM struct {
	Title string
	// Lang is the resolved request language, consumed by the shared layout
	// (html lang attribute, header language toggle, footer) and by the
	// template's own {{t}}/{{safeHTML}} lookups.
	Lang string
	// HasCARoot is true when a CA root certificate is configured and present on
	// disk, so the page can link to the download routes instead of explaining
	// that no reverse proxy is deployed.
	HasCARoot bool
}

// handleSetup renders the secure-access / PWA-install guide.
func (s *Server) handleSetup(c *fiber.Ctx) error {
	lang := langFromCtx(c)
	vm := setupVM{
		Title:     s.i18n.T(lang, "setup.title"),
		Lang:      lang,
		HasCARoot: s.caRootAvailable(),
	}
	return c.Render("setup", vm, "layout")
}

// caRootAvailable reports whether Web.CARootPath is set and the file it points
// at currently exists.
func (s *Server) caRootAvailable() bool {
	if s.cfg.CARootPath == "" {
		return false
	}
	_, err := os.Stat(s.cfg.CARootPath)
	return err == nil
}

// readCARoot loads the configured CA root certificate. When the deployment has
// no CA configured or the file is missing (e.g. local dev without Caddy in
// front) it returns a non-empty notFound message for the caller to surface as a
// friendly 404; a non-nil err is reserved for unexpected read failures (→ 500).
// Exactly one of (data), (notFound), (err) is meaningful.
func (s *Server) readCARoot() (data []byte, notFound string, err error) {
	if s.cfg.CARootPath == "" {
		return nil, "No CA root certificate is configured for this deployment (WHA_WEB_CAROOTPATH is unset).", nil
	}
	data, err = os.ReadFile(s.cfg.CARootPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "The configured CA root certificate file was not found on disk.", nil
		}
		return nil, "", fmt.Errorf("setup: read CA root: %w", err)
	}
	return data, "", nil
}

// handleSetupRootCert serves the reverse proxy's root CA certificate as a
// download. When no CARootPath is configured, or the file is missing (e.g.
// local dev without Caddy in front), it returns a friendly 404 — never a 500.
func (s *Server) handleSetupRootCert(c *fiber.Ctx) error {
	data, notFound, err := s.readCARoot()
	if err != nil {
		return err
	}
	if notFound != "" {
		return c.Status(fiber.StatusNotFound).SendString(notFound)
	}
	c.Set(fiber.HeaderContentType, "application/x-x509-ca-cert")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="root.crt"`)
	return c.Send(data)
}

// handleSetupMobileconfig serves an Apple configuration profile that packages
// the root CA certificate for a guided install on iOS/macOS. Same
// never-500-on-missing-file guard as handleSetupRootCert.
func (s *Server) handleSetupMobileconfig(c *fiber.Ctx) error {
	data, notFound, err := s.readCARoot()
	if err != nil {
		return err
	}
	if notFound != "" {
		return c.Status(fiber.StatusNotFound).SendString(notFound)
	}

	der := data
	if block, _ := pem.Decode(data); block != nil {
		der = block.Bytes
	}

	profile := buildMobileconfig(der)
	c.Set(fiber.HeaderContentType, "application/x-apple-aspen-config")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="wha-ca.mobileconfig"`)
	return c.SendString(profile)
}

// mobileconfigTemplate is an Apple configuration profile with a single
// com.apple.security.root payload. The UUIDs are fixed rather than random: the
// profile identifies the same logical payload across downloads, which lets
// iOS treat a re-download as an update to the previously installed profile
// instead of a duplicate.
const mobileconfigTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadCertificateFileName</key>
			<string>root.crt</string>
			<key>PayloadContent</key>
			<data>%s</data>
			<key>PayloadDescription</key>
			<string>Adds the wha reverse proxy's internal CA root so this device trusts its HTTPS certificate.</string>
			<key>PayloadDisplayName</key>
			<string>wha secure access root CA</string>
			<key>PayloadIdentifier</key>
			<string>io.wha.setup.rootca</string>
			<key>PayloadType</key>
			<string>com.apple.security.root</string>
			<key>PayloadUUID</key>
			<string>b7e2a6b0-2c1a-4b8b-9a3e-6a1f6e0e6b7a</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
		</dict>
	</array>
	<key>PayloadDescription</key>
	<string>Trusts the internal CA used to serve wha over HTTPS on your home network.</string>
	<key>PayloadDisplayName</key>
	<string>wha — Secure Access</string>
	<key>PayloadIdentifier</key>
	<string>io.wha.setup</string>
	<key>PayloadRemovalDisallowed</key>
	<false/>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>4f1b6c3e-9d5a-4e88-8f2e-2e5b6c1a9f3d</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`

func buildMobileconfig(der []byte) string {
	return fmt.Sprintf(mobileconfigTemplate, base64.StdEncoding.EncodeToString(der))
}
