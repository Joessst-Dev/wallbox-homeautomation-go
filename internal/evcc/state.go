package evcc

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// FloatMetric is a numeric value with the wall-clock time it was last received.
// Seen distinguishes "never received" from "received an old value".
type FloatMetric struct {
	Value float64
	At    time.Time
	Seen  bool
}

// BoolMetric is a boolean value with its receipt time.
type BoolMetric struct {
	Value bool
	At    time.Time
	Seen  bool
}

// StringMetric is a string value with its receipt time.
type StringMetric struct {
	Value string
	At    time.Time
	Seen  bool
}

// Snapshot is an immutable copy of the latest evcc state plus connection health.
type Snapshot struct {
	Grid         FloatMetric
	PV           FloatMetric
	Home         FloatMetric
	BatteryPower FloatMetric
	BatterySoC   FloatMetric
	ChargePower  FloatMetric
	VehicleSoC   FloatMetric
	LimitSoC     FloatMetric

	Charging  BoolMetric
	Connected BoolMetric
	Enabled   BoolMetric
	Mode      StringMetric

	// Online reflects evcc's MQTT LWT (evcc/status == "online").
	Online BoolMetric
	// BrokerConnected is our own MQTT client connection state.
	BrokerConnected bool
}

// Store is the thread-safe latest-known evcc state, updated by the MQTT client
// and read by the control loop.
type Store struct {
	topics Topics
	mu     sync.RWMutex
	snap   Snapshot
}

// NewStore returns an empty Store bound to the given topic set.
func NewStore(t Topics) *Store {
	return &Store{topics: t}
}

// Snapshot returns a copy of the current state.
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

func (s *Store) setBrokerConnected(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.BrokerConnected = v
}

// Apply routes one received MQTT message into the snapshot. Unknown topics and
// unparseable payloads are ignored (returns false) so malformed data can never
// panic the client. at is the receipt time.
func (s *Store) Apply(topic, payload string, at time.Time) bool {
	t := s.topics
	s.mu.Lock()
	defer s.mu.Unlock()

	switch topic {
	case t.GridPower:
		return setFloat(&s.snap.Grid, payload, at)
	case t.PVPower:
		return setFloat(&s.snap.PV, payload, at)
	case t.HomePower:
		return setFloat(&s.snap.Home, payload, at)
	case t.BatteryPower:
		return setFloat(&s.snap.BatteryPower, payload, at)
	case t.BatterySoC:
		return setFloat(&s.snap.BatterySoC, payload, at)
	case t.ChargePower:
		return setFloat(&s.snap.ChargePower, payload, at)
	case t.VehicleSoC:
		return setFloat(&s.snap.VehicleSoC, payload, at)
	case t.LimitSoC:
		return setFloat(&s.snap.LimitSoC, payload, at)
	case t.Charging:
		return setBool(&s.snap.Charging, payload, at)
	case t.Connected:
		return setBool(&s.snap.Connected, payload, at)
	case t.Enabled:
		return setBool(&s.snap.Enabled, payload, at)
	case t.Mode:
		s.snap.Mode = StringMetric{Value: payload, At: at, Seen: true}
		return true
	case t.Status:
		s.snap.Online = BoolMetric{Value: payload == "online", At: at, Seen: true}
		return true
	default:
		return false
	}
}

// setFloat parses an evcc numeric payload (trimmed decimal string) into field.
func setFloat(field *FloatMetric, payload string, at time.Time) bool {
	v, err := strconv.ParseFloat(strings.TrimSpace(payload), 64)
	if err != nil {
		return false
	}
	*field = FloatMetric{Value: v, At: at, Seen: true}
	return true
}

// setBool parses an evcc boolean payload. evcc encodes these as the literal
// strings "true"/"false" (Go's %v of a bool), not 1/0.
func setBool(field *BoolMetric, payload string, at time.Time) bool {
	switch strings.TrimSpace(payload) {
	case "true":
		*field = BoolMetric{Value: true, At: at, Seen: true}
	case "false":
		*field = BoolMetric{Value: false, At: at, Seen: true}
	default:
		return false
	}
	return true
}
