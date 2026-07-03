// Package config defines the wha configuration model, its defaults, and
// validation. Values are layered by the CLI as: explicit flag > WHA_* env >
// config file > built-in defaults.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
	yaml "go.yaml.in/yaml/v3"
)

const envPrefix = "WHA"

// Load builds the effective configuration from (lowest to highest precedence):
// built-in defaults, a YAML config file, and WHA_* environment variables.
//
// The config file path comes from WHA_CONFIG; if unset, "config.yaml" is searched
// for in /etc/wha and the working directory. Env keys are WHA_<SECTION>_<KEY> with
// nested camelCase keys uppercased fully, e.g. control.startThresholdW →
// WHA_CONTROL_STARTTHRESHOLDW. Durations may be written as "60s"/"2m".
func Load() (Config, error) {
	v := viper.New()
	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Seed viper with every key from Default() so AutomaticEnv can override even
	// nested keys (viper only consults env for keys it already knows about).
	seed, err := yaml.Marshal(Default())
	if err != nil {
		return Config{}, fmt.Errorf("marshal default config: %w", err)
	}
	v.SetConfigType("yaml")
	if err := v.MergeConfig(bytes.NewReader(seed)); err != nil {
		return Config{}, fmt.Errorf("seed defaults: %w", err)
	}

	// Overlay a config file if one is present.
	if path := os.Getenv(envPrefix + "_CONFIG"); path != "" {
		v.SetConfigFile(path)
		if err := v.MergeInConfig(); err != nil {
			return Config{}, fmt.Errorf("read config %s: %w", path, err)
		}
	} else {
		v.SetConfigName("config")
		v.AddConfigPath("/etc/wha")
		v.AddConfigPath(".")
		if err := v.MergeInConfig(); err != nil {
			var notFound viper.ConfigFileNotFoundError
			if !errors.As(err, &notFound) {
				return Config{}, fmt.Errorf("read config: %w", err)
			}
		}
	}

	cfg := Default()
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

// Config is the fully-resolved application configuration.
type Config struct {
	MQTT    MQTT    `mapstructure:"mqtt"`
	EVCC    EVCC    `mapstructure:"evcc"`
	Control Control `mapstructure:"control"`
	Web     Web     `mapstructure:"web"`
	DB      DB      `mapstructure:"db"`
	Log     Log     `mapstructure:"log"`
	Update  Update  `mapstructure:"update"`
}

// MQTT holds the connection settings for the Mosquitto broker that evcc
// publishes to.
type MQTT struct {
	Broker      string `mapstructure:"broker"`
	ClientID    string `mapstructure:"clientID"`
	Username    string `mapstructure:"username"`
	Password    string `mapstructure:"password"`
	TopicPrefix string `mapstructure:"topicPrefix"`
}

// EVCC identifies which evcc loadpoint wha controls.
type EVCC struct {
	LoadpointID string `mapstructure:"loadpointID"`
}

// Control holds the decision-policy tuning parameters.
type Control struct {
	// EnableMode is the evcc mode published when charging is enabled.
	// "pv" lets evcc modulate current to the actual surplus (no grid import);
	// "now" charges at full available power.
	EnableMode string `mapstructure:"enableMode"`

	StartThresholdW float64 `mapstructure:"startThresholdW"`
	StopThresholdW  float64 `mapstructure:"stopThresholdW"`

	StartDwell time.Duration `mapstructure:"startDwell"`
	StopDwell  time.Duration `mapstructure:"stopDwell"`

	// SoCCap stops charging once vehicle SoC reaches this value. SoCResumeBelow
	// is the lower latch boundary that allows charging to resume.
	SoCCap         int `mapstructure:"socCap"`
	SoCResumeBelow int `mapstructure:"socResumeBelow"`

	// SoCMax is the ceiling used when an operator explicitly opts to charge past
	// SoCCap (the Charge-now "charge past the cap" option). The evcc limitSoc
	// backstop is lifted to this value only while such an override is active.
	SoCMax int `mapstructure:"socMax"`

	DecisionInterval time.Duration `mapstructure:"decisionInterval"`

	// StaleTimeout applies to the fast-moving power metrics (grid/pv/battery/
	// charge power) only. Vehicle SoC is deliberately NOT stale-gated: evcc
	// polls the Renault cloud for SoC at most ~hourly and only while charging,
	// so the last known value is always used for the SoC cap (the evcc limitSoc
	// backstop bounds any overshoot).
	StaleTimeout time.Duration `mapstructure:"staleTimeout"`

	Republish time.Duration `mapstructure:"republish"`

	// RetentionWindow bounds how long samples/events are kept; rows older than
	// this are pruned by the janitor. Zero disables pruning. RetentionInterval is
	// how often the janitor runs.
	RetentionWindow   time.Duration `mapstructure:"retentionWindow"`
	RetentionInterval time.Duration `mapstructure:"retentionInterval"`
}

// Web holds the HTTP server settings.
type Web struct {
	BindAddr string `mapstructure:"bindAddr"`
	Port     int    `mapstructure:"port"`
}

// DB holds persistence settings.
type DB struct {
	Path string `mapstructure:"path"`
}

// Log holds logging settings.
type Log struct {
	Level string `mapstructure:"level"`
}

// Update holds the in-UI software-update settings. wha itself never touches the
// Docker socket: when enabled it checks GHCR for a newer release and hands the
// chosen version to the compose-aware "wha-updater" sidecar through a request
// file in RequestDir (a shared volume), reading the sidecar's progress back from
// status.json in the same directory. Disabled by default — it only works when
// the sidecar is deployed.
type Update struct {
	Enabled    bool          `mapstructure:"enabled"`
	Repository string        `mapstructure:"repository"`
	RequestDir string        `mapstructure:"requestDir"`
	CheckTTL   time.Duration `mapstructure:"checkTTL"`
}

// Charge-enable modes published to evcc.
const (
	EnableModePV  = "pv"
	EnableModeNow = "now"
)

// Default returns a Config populated with sensible defaults for a single-Easee,
// PV-surplus setup. The CLI overlays file/env/flag values on top of this.
func Default() Config {
	return Config{
		MQTT: MQTT{
			Broker:      "tcp://localhost:1883",
			ClientID:    "wha",
			TopicPrefix: "evcc",
		},
		EVCC: EVCC{
			LoadpointID: "1",
		},
		Control: Control{
			EnableMode:        EnableModePV,
			StartThresholdW:   1400, // ~min charge power, single-phase Twingo
			StopThresholdW:    0,
			StartDwell:        2 * time.Minute, // generous: protect Easee from cloud-latency churn
			StopDwell:         3 * time.Minute, // matches evcc's own disableDelay
			SoCCap:            80,
			SoCResumeBelow:    78,
			SoCMax:            100,
			DecisionInterval:  15 * time.Second,
			StaleTimeout:      60 * time.Second,
			Republish:         5 * time.Minute,
			RetentionWindow:   90 * 24 * time.Hour, // 90 days; 0 disables pruning
			RetentionInterval: 6 * time.Hour,
		},
		Web: Web{
			BindAddr: "0.0.0.0",
			Port:     8080,
		},
		DB: DB{
			Path: "/data/wha.db",
		},
		Log: Log{
			Level: "info",
		},
		Update: Update{
			Enabled:    false,
			Repository: "joessst-dev/wha",
			RequestDir: "/run/update",
			CheckTTL:   1 * time.Hour,
		},
	}
}

// Validate reports the first configuration inconsistency it finds.
func (c Config) Validate() error {
	if c.MQTT.Broker == "" {
		return fmt.Errorf("mqtt.broker must not be empty")
	}
	if c.MQTT.ClientID == "" {
		return fmt.Errorf("mqtt.clientID must not be empty")
	}
	if c.EVCC.LoadpointID == "" {
		return fmt.Errorf("evcc.loadpointID must not be empty")
	}
	if c.Control.EnableMode != EnableModePV && c.Control.EnableMode != EnableModeNow {
		return fmt.Errorf("control.enableMode must be %q or %q, got %q",
			EnableModePV, EnableModeNow, c.Control.EnableMode)
	}
	if c.Control.StartThresholdW <= c.Control.StopThresholdW {
		return fmt.Errorf("control.startThresholdW (%.0f) must be greater than stopThresholdW (%.0f)",
			c.Control.StartThresholdW, c.Control.StopThresholdW)
	}
	if c.Control.SoCCap < 1 || c.Control.SoCCap > 100 {
		return fmt.Errorf("control.socCap must be within 1..100, got %d", c.Control.SoCCap)
	}
	if c.Control.SoCResumeBelow >= c.Control.SoCCap {
		return fmt.Errorf("control.socResumeBelow (%d) must be less than socCap (%d)",
			c.Control.SoCResumeBelow, c.Control.SoCCap)
	}
	if c.Control.SoCMax <= c.Control.SoCCap || c.Control.SoCMax > 100 {
		return fmt.Errorf("control.socMax (%d) must be greater than socCap (%d) and at most 100",
			c.Control.SoCMax, c.Control.SoCCap)
	}
	if c.Control.DecisionInterval <= 0 {
		return fmt.Errorf("control.decisionInterval must be positive")
	}
	if c.Control.StaleTimeout <= 0 {
		return fmt.Errorf("control.staleTimeout must be positive")
	}
	if c.Control.Republish <= 0 {
		return fmt.Errorf("control.republish must be positive (0 would flood evcc with commands)")
	}
	if c.Control.RetentionWindow < 0 {
		return fmt.Errorf("control.retentionWindow must not be negative (0 disables pruning)")
	}
	// retentionInterval only matters when pruning is enabled; don't reject a
	// retentionInterval of 0 when the user has disabled pruning (retentionWindow 0).
	if c.Control.RetentionWindow > 0 && c.Control.RetentionInterval <= 0 {
		return fmt.Errorf("control.retentionInterval must be positive (required when retentionWindow > 0)")
	}
	if c.Control.StartDwell < 0 || c.Control.StopDwell < 0 {
		return fmt.Errorf("control.startDwell and stopDwell must not be negative")
	}
	if c.Web.Port < 1 || c.Web.Port > 65535 {
		return fmt.Errorf("web.port must be within 1..65535, got %d", c.Web.Port)
	}
	if c.DB.Path == "" {
		return fmt.Errorf("db.path must not be empty")
	}
	if c.Update.Enabled {
		if c.Update.Repository == "" {
			return fmt.Errorf("update.repository must not be empty when update.enabled is true")
		}
		if c.Update.RequestDir == "" {
			return fmt.Errorf("update.requestDir must not be empty when update.enabled is true")
		}
		if c.Update.CheckTTL < 0 {
			return fmt.Errorf("update.checkTTL must not be negative")
		}
	}
	return nil
}
