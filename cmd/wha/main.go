// Command wha is the wallbox home automation controller. It sits on top of an
// evcc instance, reads live state over MQTT, applies a PV-surplus + SoC-limit
// charging policy, and exposes a web dashboard.
//
// It is a single run-everything binary: on startup it applies database
// migrations, then runs the control loop and web server until signaled.
// Configuration comes from WHA_* environment variables and an optional YAML
// config file (WHA_CONFIG, or config.yaml in /etc/wha or the working directory).
package main

import (
	"context"
	"fmt"
	"os"

	// tzdata is embedded so local-time formatting works on minimal base images
	// (distroless/scratch ship no zoneinfo).
	_ "time/tzdata"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/app"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
)

// Build-time variables, injected via -ldflags by GoReleaser.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Printf("wha %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := app.Run(context.Background(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
