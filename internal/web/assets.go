package web

import "embed"

// embeddedFS holds the HTML templates and static assets compiled into the
// binary. The partials subdirectory must be included explicitly because
// the embed glob does not recurse into subdirectories matched only by a trailing
// "/*" on the parent — listing templates/* and templates/partials/* keeps the
// nested status/sessions fragments available to the template engine.
//
//go:embed templates/* templates/partials/* static/*
var embeddedFS embed.FS
