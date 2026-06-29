---
name: tailwind-build-workflow
description: How to (re)compile the embedded Tailwind CSS for the web dashboard — no Node required
metadata:
  type: project
---

The dashboard CSS is compiled Tailwind v3 committed to `internal/web/static/app.css` (embedded via go:embed). The Docker build is Node-free, so CSS must be compiled locally and committed whenever templates change.

**Why:** Runs on a Raspberry Pi; the binary embeds templates + static assets, so `app.css` must be real compiled output, not a placeholder.

**How to apply:** After editing any `internal/web/templates/**/*.html`, recompile so new utility classes exist:
```
cd internal/web/tailwind && ./tailwindcss -c tailwind.config.js -i input.css -o ../static/app.css --minify
```
- Standalone CLI binary lives at `internal/web/tailwind/tailwindcss` (macOS arm64, gitignored via local `.gitignore`). If missing, download from github.com/tailwindlabs/tailwindcss/releases (v3.4.17 used) and `chmod +x`.
- Config scans `../templates/**/*.html`; `darkMode: "media"` (no JS toggle — uses prefers-color-scheme). So dark styles need `dark:` variants.
- Verify with: `go build ./... && go test ./internal/web/...` from repo root.

Gotcha: Go `html/template` CSS context allows numeric interpolation like `style="width: {{.VehicleSoC}}%"` (no ZgotmplZ), used for SoC progress bars.
