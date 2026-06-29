package store

// MaxHistoryRows exposes the unexported Samples row cap to black-box tests.
func MaxHistoryRows() int { return maxHistoryRows }
