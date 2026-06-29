package web

import "time"

// MaxHistoryWindow exposes the unexported /api/history window cap to black-box
// tests so assertions stay in sync with the handler constant.
func MaxHistoryWindow() time.Duration { return maxHistoryWindow }
