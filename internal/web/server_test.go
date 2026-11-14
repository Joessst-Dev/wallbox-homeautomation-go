package web_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/updater"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/web"
)

// generateTestCertPEM returns a freshly self-signed CA certificate in PEM
// form, standing in for a reverse proxy's root CA in setup-route specs.
func generateTestCertPEM() []byte {
	GinkgoHelper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "wha test root"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	Expect(err).NotTo(HaveOccurred())

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// writeTestCert writes a freshly generated self-signed CA cert to a temp file
// and returns its path.
func writeTestCert() string {
	GinkgoHelper()
	path := filepath.Join(GinkgoT().TempDir(), "root.crt")
	Expect(os.WriteFile(path, generateTestCertPEM(), 0o600)).To(Succeed())
	return path
}

// fakeController is a deterministic stand-in for *controller.Controller that
// records the last SetOverride call so specs can assert on it.
type fakeController struct {
	status controller.StatusView

	overrideCalled       bool
	overrideMode         controller.Override
	overrideUntil        time.Time
	overrideCapBypass    bool
	overrideCapBypassSoC int

	chargePowerCalled bool
	chargePowerMode   string
	chargePowerErr    error

	autoProfileCalled bool
	autoProfileValue  string
	autoProfileErr    error

	startThresholdCalled bool
	startThresholdValue  float64
	startThresholdErr    error

	targetSoCCalled bool
	targetSoCValue  int
	targetSoCErr    error

	maxCurrentCalled bool
	maxCurrentValue  int
	maxCurrentErr    error
}

func (f *fakeController) Status() controller.StatusView { return f.status }

func (f *fakeController) SetOverride(mode controller.Override, until time.Time, capBypass bool, capBypassSoC int) {
	f.overrideCalled = true
	f.overrideMode = mode
	f.overrideUntil = until
	f.overrideCapBypass = capBypass
	f.overrideCapBypassSoC = capBypassSoC
}

func (f *fakeController) SetChargePower(mode string) error {
	f.chargePowerCalled = true
	f.chargePowerMode = mode
	return f.chargePowerErr
}

func (f *fakeController) SetAutoProfile(profile string) error {
	f.autoProfileCalled = true
	f.autoProfileValue = profile
	return f.autoProfileErr
}

func (f *fakeController) SetStartThreshold(w float64) error {
	f.startThresholdCalled = true
	f.startThresholdValue = w
	return f.startThresholdErr
}

func (f *fakeController) SetTargetSoC(pct int) error {
	f.targetSoCCalled = true
	f.targetSoCValue = pct
	return f.targetSoCErr
}

func (f *fakeController) SetMaxCurrent(amps int) error {
	f.maxCurrentCalled = true
	f.maxCurrentValue = amps
	return f.maxCurrentErr
}

// fakeUpdater is a deterministic stand-in for *updater.Updater. It records the
// last Trigger call and the number of Check calls so specs can assert on them.
type fakeUpdater struct {
	info updater.Info

	checkCalls int
	checkErr   error

	triggerCalled  bool
	triggerVersion string
	triggerErr     error
}

func (f *fakeUpdater) Info(context.Context) updater.Info { return f.info }

func (f *fakeUpdater) Check(context.Context) (updater.Info, error) {
	f.checkCalls++
	return f.info, f.checkErr
}

func (f *fakeUpdater) Trigger(_ context.Context, version string) error {
	f.triggerCalled = true
	f.triggerVersion = version
	return f.triggerErr
}

// fakeStore is an in-memory stand-in for *store.Store.
type fakeStore struct {
	sessions []store.Session
	events   []store.Event
	samples  []store.Sample
	err      error

	// gotFrom/gotTo record the last Samples range so specs can assert clamping.
	gotFrom time.Time
	gotTo   time.Time
}

func (f *fakeStore) RecentSessions(_ context.Context, limit int) ([]store.Session, error) {
	if f.err != nil {
		return nil, f.err
	}
	if limit < len(f.sessions) {
		return f.sessions[:limit], nil
	}
	return f.sessions, nil
}

func (f *fakeStore) RecentEvents(_ context.Context, limit int) ([]store.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	if limit < len(f.events) {
		return f.events[:limit], nil
	}
	return f.events, nil
}

func (f *fakeStore) Samples(_ context.Context, from, to time.Time) ([]store.Sample, error) {
	f.gotFrom = from
	f.gotTo = to
	if f.err != nil {
		return nil, f.err
	}
	return f.samples, nil
}

// doRequest runs a request through the Fiber app and returns the response.
func doRequest(srv *web.Server, req *http.Request) *http.Response {
	GinkgoHelper()
	resp, err := srv.App().Test(req, -1)
	Expect(err).NotTo(HaveOccurred())
	return resp
}

// bodyString drains and returns a response body as a string.
func bodyString(resp *http.Response) string {
	GinkgoHelper()
	b, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	_ = resp.Body.Close()
	return string(b)
}

// enRequest pins the dashboard language to English via the lang cookie, so
// specs that assert on rendered copy don't depend on the German default.
func enRequest(req *http.Request) *http.Request {
	req.AddCookie(&http.Cookie{Name: "lang", Value: "en"})
	return req
}

var _ = Describe("Web Server", func() {
	var (
		ctrl *fakeController
		st   *fakeStore
		upd  *fakeUpdater
		srv  *web.Server
	)

	BeforeEach(func() {
		ctrl = &fakeController{
			status: controller.StatusView{
				State:       controller.StateCharging,
				DesiredMode: config.EnableModePV,
				Reason:      "surplus sufficient",
				Override:    controller.OverrideAuto,
				ChargePower: config.EnableModePV,
				SoCCap:      80,
				SoCMax:      100,
				Surplus:     2500,
				Inputs: controller.Inputs{
					GridW: -2500, PVW: 5000, HomeW: 1200, BatteryW: -300, ChargeW: 3100,
					BatterySoC: 90, VehicleSoC: 55,
					Charging: true, Connected: true, Ready: true,
				},
				Snapshot: evcc.Snapshot{
					BrokerConnected: true,
					Online:          evcc.BoolMetric{Value: true, Seen: true},
				},
				UpdatedAt: time.Now().Add(-3 * time.Second),
			},
		}
		st = &fakeStore{}
		upd = &fakeUpdater{
			info: updater.Info{
				Enabled:         true,
				Current:         "1.2.3",
				Latest:          "1.3.0",
				UpdateAvailable: true,
				CheckedAt:       time.Now().Add(-time.Minute),
			},
		}
		srv = web.New(config.Web{BindAddr: "127.0.0.1", Port: 0}, ctrl, st, upd, nil)
	})

	Describe("GET /healthz", func() {
		It("always returns 200 ok", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(bodyString(resp)).To(Equal("ok"))
		})
	})

	Describe("GET /readyz", func() {
		Context("when the broker is connected", func() {
			It("returns 200", func() {
				resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/readyz", nil))
				Expect(resp.StatusCode).To(Equal(http.StatusOK))
			})
		})

		Context("when the broker is disconnected", func() {
			It("returns 503", func() {
				ctrl.status.Snapshot.BrokerConnected = false
				resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/readyz", nil))
				Expect(resp.StatusCode).To(Equal(http.StatusServiceUnavailable))
			})
		})
	})

	Describe("GET /api/status", func() {
		It("returns JSON containing the state and surplus from the controller", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/api/status", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("Content-Type")).To(ContainSubstring("application/json"))

			var vm map[string]any
			Expect(json.Unmarshal([]byte(bodyString(resp)), &vm)).To(Succeed())
			Expect(vm["state"]).To(Equal("charging"))
			Expect(vm["surplusW"]).To(BeNumerically("==", 2500))
			Expect(vm["brokerConnected"]).To(BeTrue())
		})

		It("exposes the max-current limit, stall flag, start-threshold floor, and SoC freshness", func() {
			now := time.Now()
			ctrl.status.StartThresholdW = 5720
			ctrl.status.ConfiguredStartThresholdW = 1400
			ctrl.status.MinChargePowerW = 5520
			ctrl.status.MaxCurrentA = 8
			ctrl.status.MinCurrentA = 6
			ctrl.status.MaxCurrentCeilingA = 16
			ctrl.status.Stalled = true
			ctrl.status.StalledSince = now.Add(-4 * time.Minute)
			ctrl.status.StallRecoveryActive = true
			ctrl.status.VehicleSoCChangedAt = now.Add(-20 * time.Minute)
			ctrl.status.VehicleSoCEstimated = true
			ctrl.status.Snapshot.MaxCurrent = evcc.FloatMetric{Value: 8, Seen: true}
			ctrl.status.Snapshot.ChargerStatusReason = evcc.StringMetric{Value: "waitingforauthorization", Seen: true}

			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/api/status", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var vm map[string]any
			Expect(json.Unmarshal([]byte(bodyString(resp)), &vm)).To(Succeed())

			Expect(vm["startThresholdW"]).To(BeNumerically("==", 5720))
			Expect(vm["configuredStartThresholdW"]).To(BeNumerically("==", 1400))
			Expect(vm["minChargePowerW"]).To(BeNumerically("==", 5520))
			Expect(vm["startThresholdFloored"]).To(BeTrue())

			Expect(vm["maxCurrentA"]).To(BeNumerically("==", 8))
			Expect(vm["maxCurrentLimited"]).To(BeTrue())
			Expect(vm["minCurrentA"]).To(BeNumerically("==", 6))
			Expect(vm["maxCurrentCeilingA"]).To(BeNumerically("==", 16))
			Expect(vm["evccMaxCurrentA"]).To(BeNumerically("==", 8))

			Expect(vm["stalled"]).To(BeTrue())
			Expect(vm["stalledSince"]).To(Equal(ctrl.status.StalledSince.UTC().Format(time.RFC3339)))
			Expect(vm["stalledAgo"]).To(MatchRegexp(`^[34]m$`))
			Expect(vm["stallRecoveryActive"]).To(BeTrue())
			Expect(vm["chargerStatusReason"]).To(Equal("waitingforauthorization"))

			Expect(vm["vehicleSoCChangedAt"]).To(Equal(ctrl.status.VehicleSoCChangedAt.UTC().Format(time.RFC3339)))
			Expect(vm["vehicleSoCEstimated"]).To(BeTrue())
		})

		It("reports a limit held at the ceiling (a cleared limit) as not limited", func() {
			ctrl.status.MaxCurrentA = 16
			ctrl.status.MaxCurrentCeilingA = 16
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/api/status", nil))
			var vm map[string]any
			Expect(json.Unmarshal([]byte(bodyString(resp)), &vm)).To(Succeed())
			Expect(vm["maxCurrentA"]).To(BeNumerically("==", 16))
			Expect(vm["maxCurrentLimited"]).To(BeFalse())
		})

		It("reports no limit, no stall, and no SoC change time when none apply", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/api/status", nil))
			var vm map[string]any
			Expect(json.Unmarshal([]byte(bodyString(resp)), &vm)).To(Succeed())

			Expect(vm["maxCurrentA"]).To(BeNumerically("==", 0))
			Expect(vm["maxCurrentLimited"]).To(BeFalse())
			Expect(vm["evccMaxCurrentA"]).To(BeNumerically("==", 0))
			Expect(vm["stalled"]).To(BeFalse())
			Expect(vm).NotTo(HaveKey("stalledSince"))
			Expect(vm).NotTo(HaveKey("vehicleSoCChangedAt"))
			Expect(vm["vehicleSoCEstimated"]).To(BeFalse())
			Expect(vm["startThresholdFloored"]).To(BeFalse())
		})
	})

	Describe("GET /", func() {
		It("renders the read-only status dashboard with key labels", func() {
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/", nil)))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring("Surplus"))
			Expect(body).To(ContainSubstring("Vehicle SoC"))
			Expect(body).To(ContainSubstring(`hx-get="/partials/status"`))
			// The status dashboard links to the Control and Settings tabs.
			Expect(body).To(ContainSubstring(`href="/control"`))
			Expect(body).To(ContainSubstring(`href="/settings"`))
			// Operator controls moved to the Control tab; the dashboard is read-only.
			Expect(body).NotTo(ContainSubstring(`hx-post="/api/override"`),
				"override controls moved to /control")
			Expect(body).NotTo(ContainSubstring("Auto-charge profile"),
				"auto-profile selector moved to /control")
			Expect(body).NotTo(ContainSubstring(`hx-get="/partials/update"`),
				"update card lives on /settings")
		})
	})

	Describe("GET /control", func() {
		It("renders the control page with the status strip and operator controls", func() {
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/control", nil)))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring(`hx-get="/partials/control-status"`))
			Expect(body).To(ContainSubstring(`id="controls"`))
			Expect(body).To(ContainSubstring(`hx-get="/partials/controls"`))
			Expect(body).To(ContainSubstring(`id="settings-controls"`))
			Expect(body).To(ContainSubstring(`hx-get="/partials/settings"`))
			Expect(body).To(ContainSubstring("Auto-charge profile"))
			Expect(body).To(ContainSubstring("Override"))
		})
	})

	Describe("GET /partials/control-status", func() {
		It("renders the compact status strip without the layout or controls", func() {
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/partials/control-status", nil)))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).NotTo(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring("Surplus"))
			Expect(body).To(ContainSubstring("Vehicle SoC"))
			Expect(body).NotTo(ContainSubstring(`hx-post="/api/override"`),
				"the override form belongs in controls, not the status strip")
		})
	})

	Describe("GET /settings", func() {
		It("renders the settings page with the update card and secure-access link", func() {
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/settings", nil)))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring(`hx-get="/partials/update"`))
			Expect(body).To(ContainSubstring(`href="/setup"`))
			// The auto-profile selector moved to the Control tab.
			Expect(body).NotTo(ContainSubstring(`hx-get="/partials/settings"`),
				"auto-profile selector moved to /control")
			Expect(body).NotTo(ContainSubstring(`id="settings-controls"`),
				"auto-profile selector moved to /control")
		})
	})

	Describe("GET /partials/settings", func() {
		It("renders the auto-charge configuration fragment without the layout", func() {
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/partials/settings", nil)))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).NotTo(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring("Auto-charge profile"))
			Expect(body).To(ContainSubstring(`hx-post="/api/auto-profile"`))
			Expect(body).To(ContainSubstring(`hx-target="#settings-controls"`))
		})

		It("shows the effective start threshold and minimum charge power for the surplus profile", func() {
			ctrl.status.AutoProfile = config.AutoProfileSurplus
			ctrl.status.StartThresholdW = 5720
			ctrl.status.ConfiguredStartThresholdW = 1400
			ctrl.status.MinChargePowerW = 5520
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/partials/settings", nil)))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(bodyString(resp)).To(ContainSubstring("Starts charging at 5720 W surplus — the car needs at least 5520 W."))
		})

		It("does not show the surplus-profile line for a latch profile", func() {
			ctrl.status.AutoProfile = config.AutoProfilePowerLatch
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/partials/settings", nil)))
			Expect(bodyString(resp)).NotTo(ContainSubstring("Starts charging at"))
		})
	})

	Describe("GET /partials/status", func() {
		It("renders the status fragment without the layout or controls", func() {
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/partials/status", nil)))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).NotTo(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring("Surplus"))
			Expect(body).To(ContainSubstring("Charging"))
			Expect(body).NotTo(ContainSubstring(`hx-post="/api/override"`),
				"override form must not be in the status partial (it belongs in controls)")
		})
	})

	Describe("GET /partials/detail", func() {
		It("renders the secondary detail fragment (power flow + health) without the layout", func() {
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/partials/detail", nil)))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).NotTo(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring("Power flow"))
			Expect(body).To(ContainSubstring("Health"))
			Expect(body).To(ContainSubstring("MQTT broker"))
			Expect(body).NotTo(ContainSubstring(`hx-post="/api/override"`),
				"the override form belongs in controls, not the detail partial")
		})
	})

	Describe("GET /partials/controls", func() {
		It("renders the controls fragment without the layout", func() {
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/partials/controls", nil)))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).NotTo(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring("Override"))
			Expect(body).To(ContainSubstring("Charge power"))
			Expect(body).To(ContainSubstring(`hx-post="/api/override"`))
			Expect(body).To(ContainSubstring(`hx-target="#controls"`))
			Expect(body).NotTo(ContainSubstring("Auto-charge profile"),
				"auto-profile selector moved to the settings partial")
		})

		It("shows the cap-bypass target when a specific SoC is active", func() {
			ctrl.status.CapBypass = true
			ctrl.status.CapBypassSoC = 95
			ctrl.status.Override = controller.OverrideForceOn

			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/partials/controls", nil))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("95"))
		})
	})

	Describe("new-feature markup", func() {
		get := func(path string) string {
			return bodyString(doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, path, nil))))
		}

		It("renders the max-current form bounded by the accepted range and showing the limit", func() {
			ctrl.status.MinCurrentA = 6
			ctrl.status.MaxCurrentCeilingA = 16
			ctrl.status.MaxCurrentA = 10
			body := get("/partials/controls")
			Expect(body).To(ContainSubstring(`hx-post="/api/max-current"`))
			Expect(body).To(ContainSubstring(`name="amps" min="6" max="16"`))
			Expect(body).To(ContainSubstring(`value="10"`))
			Expect(body).To(ContainSubstring("limit: 10 A"))
			Expect(body).To(ContainSubstring(`hx-vals='{"amps":"0"}'`))
		})

		It("shows no limit and no clear button when unlimited", func() {
			ctrl.status.MinCurrentA = 6
			ctrl.status.MaxCurrentCeilingA = 16
			body := get("/partials/controls")
			Expect(body).To(ContainSubstring("no wha limit"))
			Expect(body).NotTo(ContainSubstring(`hx-vals='{"amps":"0"}'`))
		})

		It("shows the stalled alert only when stalled", func() {
			for _, p := range []string{"/partials/status", "/partials/control-status"} {
				Expect(get(p)).NotTo(ContainSubstring("Charging stalled"))
			}
			ctrl.status.Stalled = true
			ctrl.status.StalledSince = time.Now().Add(-4 * time.Minute)
			ctrl.status.StallRecoveryActive = true
			ctrl.status.Snapshot.ChargerStatusReason = evcc.StringMetric{Value: "waitingforauthorization", Seen: true}
			for _, p := range []string{"/partials/status", "/partials/control-status"} {
				body := get(p)
				Expect(body).To(ContainSubstring("Charging stalled"))
				Expect(body).To(ContainSubstring("waitingforauthorization"))
				Expect(body).To(ContainSubstring("pausing charging briefly"))
			}
		})

		It("shows the estimated SoC label only when flagged", func() {
			for _, p := range []string{"/partials/status", "/partials/control-status"} {
				Expect(get(p)).NotTo(ContainSubstring("data-soc-estimated"))
			}
			ctrl.status.VehicleSoCEstimated = true
			ctrl.status.VehicleSoCChangedAt = time.Now().Add(-20 * time.Minute)
			for _, p := range []string{"/partials/status", "/partials/control-status"} {
				Expect(get(p)).To(ContainSubstring("data-soc-estimated"))
			}
		})

		It("binds the start-threshold input to the configured value and hints when floored", func() {
			ctrl.status.AutoProfile = config.AutoProfilePowerLatch
			ctrl.status.ConfiguredStartThresholdW = 1400
			ctrl.status.StartThresholdW = 5720
			ctrl.status.MinChargePowerW = 5520
			body := get("/partials/settings")
			Expect(body).To(ContainSubstring(`value="1400"`))
			Expect(body).NotTo(ContainSubstring(`value="5720"`))
			Expect(body).To(ContainSubstring("Effective start threshold 5720 W"))

			ctrl.status.StartThresholdW = 1400
			Expect(get("/partials/settings")).NotTo(ContainSubstring("Effective start threshold"))
		})
	})

	Describe("POST /api/override", func() {
		Context("with form mode=off and no htmx header", func() {
			It("sets OverrideForceOff and returns JSON ok", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/override",
					strings.NewReader("mode=off"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

				resp := doRequest(srv, req)
				Expect(resp.StatusCode).To(Equal(http.StatusOK))
				Expect(ctrl.overrideCalled).To(BeTrue())
				Expect(ctrl.overrideMode).To(Equal(controller.OverrideForceOff))
				Expect(ctrl.overrideUntil.IsZero()).To(BeTrue())
				Expect(bodyString(resp)).To(MatchJSON(`{"ok":true}`))
			})
		})

		Context("with an HX-Request header", func() {
			It("returns the refreshed controls partial", func() {
				req := enRequest(httptest.NewRequest(http.MethodPost, "/api/override",
					strings.NewReader("mode=off")))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("HX-Request", "true")

				resp := doRequest(srv, req)
				Expect(resp.StatusCode).To(Equal(http.StatusOK))
				Expect(ctrl.overrideMode).To(Equal(controller.OverrideForceOff))
				body := bodyString(resp)
				Expect(body).NotTo(ContainSubstring("<!DOCTYPE html>"))
				Expect(body).To(ContainSubstring("Override"))
				Expect(body).To(ContainSubstring("Charge power"))
			})
		})

		Context("with hours > 0", func() {
			It("sets a non-zero expiry", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/override",
					strings.NewReader("mode=on&hours=2"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

				doRequest(srv, req)
				Expect(ctrl.overrideMode).To(Equal(controller.OverrideForceOn))
				Expect(ctrl.overrideUntil.IsZero()).To(BeFalse())
			})
		})

		Context("with an invalid mode", func() {
			It("returns 400", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/override",
					strings.NewReader("mode=bogus"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

				resp := doRequest(srv, req)
				Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
				Expect(ctrl.overrideCalled).To(BeFalse())
			})
		})

		Context("with capBypass on a force-on", func() {
			It("forwards capBypass=true", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/override",
					strings.NewReader("mode=on&capBypass=true"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

				doRequest(srv, req)
				Expect(ctrl.overrideMode).To(Equal(controller.OverrideForceOn))
				Expect(ctrl.overrideCapBypass).To(BeTrue())
			})
		})

		Context("with capBypass on a non-force-on mode", func() {
			It("ignores capBypass (forced false)", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/override",
					strings.NewReader("mode=off&capBypass=true"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

				doRequest(srv, req)
				Expect(ctrl.overrideMode).To(Equal(controller.OverrideForceOff))
				Expect(ctrl.overrideCapBypass).To(BeFalse())
			})
		})

		Context("with capBypassSoC on a force-on", func() {
			It("returns the controls partial for htmx and calls SetOverride with the target", func() {
				req := enRequest(httptest.NewRequest(http.MethodPost, "/api/override",
					strings.NewReader("mode=on&capBypass=true&capBypassSoC=95")))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("HX-Request", "true")

				resp := doRequest(srv, req)
				Expect(resp.StatusCode).To(Equal(http.StatusOK))
				Expect(ctrl.overrideMode).To(Equal(controller.OverrideForceOn))
				Expect(ctrl.overrideCapBypass).To(BeTrue())
				Expect(ctrl.overrideCapBypassSoC).To(Equal(95))
				body := bodyString(resp)
				Expect(body).To(ContainSubstring("Override"))
			})
		})
	})

	Describe("POST /api/charge-power", func() {
		It("sets the mode and returns JSON ok without htmx", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/charge-power",
				strings.NewReader("power=now"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(ctrl.chargePowerCalled).To(BeTrue())
			Expect(ctrl.chargePowerMode).To(Equal("now"))
			Expect(bodyString(resp)).To(MatchJSON(`{"ok":true}`))
		})

		It("returns the refreshed controls partial for htmx requests", func() {
			req := enRequest(httptest.NewRequest(http.MethodPost, "/api/charge-power",
				strings.NewReader("power=pv")))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("HX-Request", "true")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).NotTo(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring("Charge power"))
			Expect(body).To(ContainSubstring("Override"))
		})

		It("returns 400 for an invalid mode (ErrInvalidChargePower)", func() {
			ctrl.chargePowerErr = fmt.Errorf("%w: bogus", controller.ErrInvalidChargePower)
			req := httptest.NewRequest(http.MethodPost, "/api/charge-power",
				strings.NewReader("power=bogus"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		})

		It("returns 500 when persisting the mode fails", func() {
			ctrl.chargePowerErr = fmt.Errorf("persist charge power: disk full")
			req := httptest.NewRequest(http.MethodPost, "/api/charge-power",
				strings.NewReader("power=now"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
		})

		It("does not let a later request's body buffer corrupt an earlier stored mode", func() {
			// Fiber's BodyParser (non-Immutable) returns strings that alias the
			// pooled fasthttp request buffer. Firing several requests back to
			// back through the same app reuses that buffer, so a value stored
			// without copying it out (e.g. into long-lived controller state)
			// can be silently overwritten by a later request's bytes.
			first := httptest.NewRequest(http.MethodPost, "/api/charge-power",
				strings.NewReader("power=pv"))
			first.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			doRequest(srv, first)
			Expect(ctrl.chargePowerMode).To(Equal("pv"))
			stored := ctrl.chargePowerMode

			for i := 0; i < 5; i++ {
				req := httptest.NewRequest(http.MethodPost, "/api/charge-power",
					strings.NewReader("power=now"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				doRequest(srv, req)
			}

			Expect(stored).To(Equal("pv"), "earlier stored mode must not be corrupted by a later request's recycled buffer")
		})
	})

	Describe("POST /api/auto-profile", func() {
		It("sets the profile and returns JSON ok without htmx", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/auto-profile",
				strings.NewReader("profile=power_latch"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(ctrl.autoProfileCalled).To(BeTrue())
			Expect(ctrl.autoProfileValue).To(Equal("power_latch"))
			Expect(bodyString(resp)).To(MatchJSON(`{"ok":true}`))
		})

		It("returns the refreshed settings partial for htmx requests", func() {
			req := enRequest(httptest.NewRequest(http.MethodPost, "/api/auto-profile",
				strings.NewReader("profile=surplus_latch")))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("HX-Request", "true")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).NotTo(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring("Auto-charge profile"))
		})

		It("returns 400 for an invalid profile (ErrInvalidAutoProfile)", func() {
			ctrl.autoProfileErr = fmt.Errorf("%w: bogus", controller.ErrInvalidAutoProfile)
			req := httptest.NewRequest(http.MethodPost, "/api/auto-profile",
				strings.NewReader("profile=bogus"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		})

		It("returns 500 when persisting the profile fails", func() {
			ctrl.autoProfileErr = fmt.Errorf("persist auto profile: disk full")
			req := httptest.NewRequest(http.MethodPost, "/api/auto-profile",
				strings.NewReader("profile=power_latch"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
		})
	})

	Describe("POST /api/start-threshold", func() {
		It("sets the threshold and returns JSON ok without htmx", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/start-threshold",
				strings.NewReader("startThresholdW=2000"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(ctrl.startThresholdCalled).To(BeTrue())
			Expect(ctrl.startThresholdValue).To(Equal(2000.0))
			Expect(bodyString(resp)).To(MatchJSON(`{"ok":true}`))
		})

		It("returns 400 for an invalid threshold (ErrInvalidStartThreshold)", func() {
			ctrl.startThresholdErr = fmt.Errorf("%w: bogus", controller.ErrInvalidStartThreshold)
			req := httptest.NewRequest(http.MethodPost, "/api/start-threshold",
				strings.NewReader("startThresholdW=50"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		})

		It("returns 500 when persisting the threshold fails", func() {
			ctrl.startThresholdErr = fmt.Errorf("persist start threshold: disk full")
			req := httptest.NewRequest(http.MethodPost, "/api/start-threshold",
				strings.NewReader("startThresholdW=2000"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
		})
	})

	Describe("POST /api/target-soc", func() {
		It("sets the target and returns JSON ok without htmx", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/target-soc",
				strings.NewReader("targetSoC=90"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(ctrl.targetSoCCalled).To(BeTrue())
			Expect(ctrl.targetSoCValue).To(Equal(90))
			Expect(bodyString(resp)).To(MatchJSON(`{"ok":true}`))
		})

		It("returns 400 for an invalid target (ErrInvalidTargetSoC)", func() {
			ctrl.targetSoCErr = fmt.Errorf("%w: bogus", controller.ErrInvalidTargetSoC)
			req := httptest.NewRequest(http.MethodPost, "/api/target-soc",
				strings.NewReader("targetSoC=5"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		})

		It("returns 500 when persisting the target fails", func() {
			ctrl.targetSoCErr = fmt.Errorf("persist target SoC: disk full")
			req := httptest.NewRequest(http.MethodPost, "/api/target-soc",
				strings.NewReader("targetSoC=90"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
		})
	})

	Describe("POST /api/max-current", func() {
		It("sets the limit from a form body and returns JSON ok without htmx", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/max-current", strings.NewReader("amps=8"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(ctrl.maxCurrentCalled).To(BeTrue())
			Expect(ctrl.maxCurrentValue).To(Equal(8))
			Expect(bodyString(resp)).To(MatchJSON(`{"ok":true}`))
		})

		It("accepts a JSON body", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/max-current", strings.NewReader(`{"amps":10}`))
			req.Header.Set("Content-Type", "application/json")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(ctrl.maxCurrentValue).To(Equal(10))
		})

		DescribeTable("returns 400 without calling the controller when the amps key is missing",
			func(contentType, body string) {
				req := httptest.NewRequest(http.MethodPost, "/api/max-current", strings.NewReader(body))
				req.Header.Set("Content-Type", contentType)

				resp := doRequest(srv, req)
				Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
				Expect(ctrl.maxCurrentCalled).To(BeFalse())
			},
			Entry("empty JSON object", "application/json", `{}`),
			Entry("misspelled JSON key", "application/json", `{"amp":8}`),
			Entry("JSON null", "application/json", `{"amps":null}`),
			Entry("empty form body", "application/x-www-form-urlencoded", ``),
			Entry("misspelled form key", "application/x-www-form-urlencoded", `amp=8`),
		)

		It("treats a present-but-empty form value (the UI's empty input) as clear", func() {
			ctrl.maxCurrentValue = -1
			req := httptest.NewRequest(http.MethodPost, "/api/max-current", strings.NewReader("amps="))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(ctrl.maxCurrentCalled).To(BeTrue())
			Expect(ctrl.maxCurrentValue).To(Equal(0))
		})

		It("passes amps=0 through so the controller clears the limit", func() {
			ctrl.maxCurrentValue = -1
			req := httptest.NewRequest(http.MethodPost, "/api/max-current", strings.NewReader("amps=0"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(ctrl.maxCurrentCalled).To(BeTrue())
			Expect(ctrl.maxCurrentValue).To(Equal(0))
		})

		It("returns the refreshed controls partial for htmx requests", func() {
			req := enRequest(httptest.NewRequest(http.MethodPost, "/api/max-current", strings.NewReader("amps=8")))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("HX-Request", "true")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).NotTo(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring("Override"))
		})

		It("returns 400 for a non-numeric amps value without calling the controller", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/max-current", strings.NewReader("amps=lots"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
			Expect(ctrl.maxCurrentCalled).To(BeFalse())
		})

		It("returns 400 for an out-of-range limit (ErrInvalidMaxCurrent)", func() {
			ctrl.maxCurrentErr = fmt.Errorf("%w 40 A", controller.ErrInvalidMaxCurrent)
			req := httptest.NewRequest(http.MethodPost, "/api/max-current", strings.NewReader("amps=40"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		})

		It("returns 500 when persisting the limit fails", func() {
			ctrl.maxCurrentErr = fmt.Errorf("persist max current: disk full")
			req := httptest.NewRequest(http.MethodPost, "/api/max-current", strings.NewReader("amps=8"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
		})
	})

	Describe("cross-site request forgery guard on /api", func() {
		// chargePower posts a state-changing request with the given extra headers.
		chargePower := func(host string, headers map[string]string) int {
			GinkgoHelper()
			req := httptest.NewRequest(http.MethodPost, "/api/charge-power", strings.NewReader("power=now"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if host != "" {
				req.Host = host
			}
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			resp := doRequest(srv, req)
			_ = resp.Body.Close()
			return resp.StatusCode
		}

		DescribeTable("rejects cross-site state changes with 403 before the controller is touched",
			func(headers map[string]string) {
				Expect(chargePower("wha.local", headers)).To(Equal(http.StatusForbidden))
				Expect(ctrl.chargePowerCalled).To(BeFalse())
			},
			Entry("Sec-Fetch-Site: cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}),
			Entry("Sec-Fetch-Site: cross-site even with a matching Origin",
				map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://wha.local"}),
			Entry("foreign Origin", map[string]string{"Origin": "https://evil.example"}),
			Entry("foreign Origin on another port", map[string]string{"Origin": "https://wha.local:8443"}),
			Entry("opaque Origin null", map[string]string{"Origin": "null"}),
		)

		DescribeTable("allows same-origin and header-less clients",
			func(host string, headers map[string]string) {
				Expect(chargePower(host, headers)).To(Equal(http.StatusOK))
				Expect(ctrl.chargePowerCalled).To(BeTrue())
			},
			Entry("curl / scripts: no Origin, no Sec-Fetch-Site", "wha.local", map[string]string{}),
			Entry("same-origin htmx POST through Caddy (Host passed through)", "wha.local",
				map[string]string{"Origin": "https://wha.local", "Sec-Fetch-Site": "same-origin", "HX-Request": "true"}),
			Entry("Origin host matching case-insensitively", "wha.local",
				map[string]string{"Origin": "https://WHA.local"}),
			Entry("LAN IP with explicit port", "192.168.1.20:8080",
				map[string]string{"Origin": "http://192.168.1.20:8080", "Sec-Fetch-Site": "same-origin"}),
			Entry("a proxy that rewrites Host but sets X-Forwarded-Host", "wha:8080",
				map[string]string{"Origin": "https://wha.local", "X-Forwarded-Host": "wha.local"}),
			Entry("Sec-Fetch-Site: none (typed/bookmarked navigation)", "wha.local",
				map[string]string{"Sec-Fetch-Site": "none"}),
		)

		It("does not block reads", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			req.Header.Set("Origin", "https://evil.example")
			Expect(doRequest(srv, req).StatusCode).To(Equal(http.StatusOK))
		})

		It("guards every state-changing /api route, e.g. the override and max-current", func() {
			for _, path := range []string{"/api/override", "/api/max-current", "/api/auto-profile", "/api/update/apply"} {
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("mode=on&amps=8"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("Origin", "https://evil.example")
				Expect(doRequest(srv, req).StatusCode).To(Equal(http.StatusForbidden), path)
			}
			Expect(ctrl.overrideCalled).To(BeFalse())
			Expect(ctrl.maxCurrentCalled).To(BeFalse())
		})
	})

	Describe("GET /partials/update", func() {
		It("renders the update fragment with the current and latest versions", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/partials/update", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).NotTo(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring("1.2.3"))
			Expect(body).To(ContainSubstring("1.3.0"))
		})
	})

	Describe("POST /api/update/check", func() {
		It("re-checks and returns the partial for htmx requests", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/update/check", nil)
			req.Header.Set("HX-Request", "true")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(upd.checkCalls).To(Equal(1))
			body := bodyString(resp)
			Expect(body).NotTo(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring("1.3.0"))
		})

		It("returns JSON when not an htmx request", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodPost, "/api/update/check", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var vm map[string]any
			Expect(json.Unmarshal([]byte(bodyString(resp)), &vm)).To(Succeed())
			Expect(vm["latest"]).To(Equal("1.3.0"))
			Expect(vm["updateAvailable"]).To(BeTrue())
		})
	})

	Describe("POST /api/update/apply", func() {
		Context("with the latest version", func() {
			It("triggers the update and returns JSON ok", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/update/apply",
					strings.NewReader("version=1.3.0"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

				resp := doRequest(srv, req)
				Expect(resp.StatusCode).To(Equal(http.StatusOK))
				Expect(upd.triggerCalled).To(BeTrue())
				Expect(upd.triggerVersion).To(Equal("1.3.0"))
				Expect(bodyString(resp)).To(MatchJSON(`{"ok":true}`))
			})
		})

		Context("with a version other than the latest", func() {
			It("returns 400 and does not trigger", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/update/apply",
					strings.NewReader("version=9.9.9"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

				resp := doRequest(srv, req)
				Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
				Expect(upd.triggerCalled).To(BeFalse())
			})
		})

		Context("when updates are disabled", func() {
			It("returns 400", func() {
				upd.info.Enabled = false
				req := httptest.NewRequest(http.MethodPost, "/api/update/apply",
					strings.NewReader("version=1.3.0"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

				resp := doRequest(srv, req)
				Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
				Expect(upd.triggerCalled).To(BeFalse())
			})
		})
	})

	Describe("GET /api/sessions", func() {
		It("returns the sessions from the store as JSON", func() {
			ended := time.Now()
			soc := 42
			st.sessions = []store.Session{
				{ID: 7, StartedAt: time.Now().Add(-time.Hour), EndedAt: &ended,
					StartReason: "surplus", StopReason: "soc_cap",
					StartVehicleSoC: &soc, EnergyWh: 5400, AvgChargeW: 3000, PeakChargeW: 3600},
			}

			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var out []map[string]any
			Expect(json.Unmarshal([]byte(bodyString(resp)), &out)).To(Succeed())
			Expect(out).To(HaveLen(1))
			Expect(out[0]["id"]).To(BeNumerically("==", 7))
			Expect(out[0]["startReason"]).To(Equal("surplus"))
			Expect(out[0]["energyKWh"]).To(Equal("5.40"))
		})
	})

	Describe("GET /api/history", func() {
		It("clamps an over-long range to the most recent 7 days", func() {
			from := "2020-01-01T00:00:00Z" // years before to
			to := "2026-06-28T00:00:00Z"
			req := httptest.NewRequest(http.MethodGet,
				"/api/history?from="+from+"&to="+to, nil)

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			window := st.gotTo.Sub(st.gotFrom)
			Expect(window).To(BeNumerically("<=", web.MaxHistoryWindow()))
			parsedTo, err := time.Parse(time.RFC3339, to)
			Expect(err).NotTo(HaveOccurred())
			Expect(st.gotTo).To(BeTemporally("==", parsedTo))
			Expect(st.gotFrom).To(BeTemporally("==", parsedTo.Add(-web.MaxHistoryWindow())))
		})

		It("rejects to before from with 400", func() {
			req := httptest.NewRequest(http.MethodGet,
				"/api/history?from=2026-06-28T00:00:00Z&to=2026-06-27T00:00:00Z", nil)
			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		})
	})

	Describe("GET /static assets", func() {
		It("serves the embedded stylesheet", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/static/app.css", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})

		It("serves the embedded SVG favicon", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/static/favicon.svg", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("Content-Type")).To(ContainSubstring("image/svg+xml"))
		})
	})

	Describe("favicon", func() {
		It("links the SVG favicon and the apple-touch-icon from the dashboard head", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/", nil))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring(`<link rel="icon" type="image/svg+xml" href="/static/favicon.svg">`))
			Expect(body).To(ContainSubstring(`<link rel="apple-touch-icon" href="/static/apple-touch-icon.png">`))
		})

		It("permanently redirects /favicon.ico to the SVG", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/favicon.ico", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusMovedPermanently))
			Expect(resp.Header.Get("Location")).To(Equal("/static/favicon.svg"))
		})

		It("serves the embedded PNG apple-touch-icon", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/static/apple-touch-icon.png", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("Content-Type")).To(ContainSubstring("image/png"))
		})
	})

	Describe("Localization", func() {
		It("defaults to German when there is no cookie and no Accept-Language header", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/partials/status", nil))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("Überschuss"))
			Expect(body).NotTo(ContainSubstring("Surplus"))
		})

		It("renders in English when the lang cookie is en", func() {
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/partials/status", nil)))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("Surplus"))
		})

		It("renders in English when Accept-Language prefers en and there is no cookie", func() {
			req := httptest.NewRequest(http.MethodGet, "/partials/status", nil)
			req.Header.Set("Accept-Language", "en-US,en;q=0.9")
			resp := doRequest(srv, req)
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("Surplus"))
		})

		It("prefers the cookie over Accept-Language", func() {
			req := enRequest(httptest.NewRequest(http.MethodGet, "/partials/status", nil))
			req.Header.Set("Accept-Language", "de")
			resp := doRequest(srv, req)
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("Surplus"))
		})

		It("localizes the dashboard title and the sessions/update partials", func() {
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/", nil)))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("<title>wha — PV-surplus charging</title>"))
			Expect(body).To(ContainSubstring("Recent sessions"))
		})

		It("renders the localized controller reason for a known reason code", func() {
			ctrl.status.ReasonCode = controller.ReasonSurplusCharging

			enResp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/partials/status", nil)))
			Expect(bodyString(enResp)).To(ContainSubstring("Surplus sustained — charging"))

			deResp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/partials/status", nil))
			Expect(bodyString(deResp)).To(ContainSubstring("Überschuss anhaltend — lädt"))
		})

		It("falls back to the raw English reason string when the reason code is unmapped", func() {
			ctrl.status.Reason = "surplus sufficient"
			ctrl.status.ReasonCode = "some_future_code"

			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/partials/status", nil)))
			Expect(bodyString(resp)).To(ContainSubstring("surplus sufficient"))
		})

		It("falls back to the raw session reason code when it has no translation", func() {
			ended := time.Now()
			st.sessions = []store.Session{
				{ID: 1, StartedAt: time.Now().Add(-time.Hour), EndedAt: &ended,
					StartReason: "surplus", StopReason: "some_future_code"},
			}
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/partials/sessions", nil)))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("Surplus"))
			Expect(body).To(ContainSubstring("some_future_code"))
		})

		It("renders the start SoC value, not the pointer address, in the localized 'from X%' label", func() {
			soc := 42
			st.sessions = []store.Session{
				{ID: 1, StartedAt: time.Now().Add(-time.Hour), StartReason: "surplus", StartVehicleSoC: &soc},
			}
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/partials/sessions", nil)))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("from 42%"))
		})
	})

	Describe("GET /lang/:code", func() {
		It("sets the lang cookie and redirects to a same-origin Referer path", func() {
			req := httptest.NewRequest(http.MethodGet, "/lang/en", nil)
			// httptest.NewRequest defaults Host to example.com; a same-origin
			// Referer must carry that host to be honored.
			req.Header.Set("Referer", "http://example.com/some/page?x=1")
			resp := doRequest(srv, req)

			Expect(resp.StatusCode).To(Equal(http.StatusFound))
			// Only the local path+query is used, never the absolute URL.
			Expect(resp.Header.Get("Location")).To(Equal("/some/page?x=1"))

			var cookie *http.Cookie
			for _, c := range resp.Cookies() {
				if c.Name == "lang" {
					cookie = c
				}
			}
			Expect(cookie).NotTo(BeNil())
			Expect(cookie.Value).To(Equal("en"))
		})

		It("redirects to / when there is no Referer", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/lang/de", nil))
			Expect(resp.Header.Get("Location")).To(Equal("/"))
		})

		It("ignores a cross-origin Referer (no open redirect)", func() {
			req := httptest.NewRequest(http.MethodGet, "/lang/en", nil)
			req.Header.Set("Referer", "http://evil.example.net/phish")
			resp := doRequest(srv, req)
			Expect(resp.Header.Get("Location")).To(Equal("/"))
		})

		It("rejects an unsupported language code", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/lang/fr", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		})
	})

	Describe("GET /sw.js", func() {
		It("returns the service worker with the running version injected and a root-scoped header", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/sw.js", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("Service-Worker-Allowed")).To(Equal("/"))
			Expect(resp.Header.Get("Content-Type")).To(ContainSubstring("javascript"))

			body := bodyString(resp)
			Expect(body).To(ContainSubstring(`VERSION = "1.2.3"`))
			Expect(body).To(ContainSubstring("wha-shell-"))
		})

		It("falls back to \"dev\" when no build version is available", func() {
			upd.info.Current = ""
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/sw.js", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(bodyString(resp)).To(ContainSubstring(`VERSION = "dev"`))
		})
	})

	Describe("GET /static/manifest.webmanifest", func() {
		It("serves a valid manifest scoped to the whole app", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/static/manifest.webmanifest", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var manifest map[string]any
			Expect(json.Unmarshal([]byte(bodyString(resp)), &manifest)).To(Succeed())
			Expect(manifest["start_url"]).To(Equal("/"))
			Expect(manifest["scope"]).To(Equal("/"))
			Expect(manifest["display"]).To(Equal("standalone"))
		})
	})

	Describe("GET /setup", func() {
		It("renders the guide without download links when no CA root is configured", func() {
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/setup", nil)))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("Secure access"))
			Expect(body).NotTo(ContainSubstring("Download root certificate"))
		})

		It("renders download links once a CA root is configured and present", func() {
			srv = web.New(config.Web{BindAddr: "127.0.0.1", Port: 0, CARootPath: writeTestCert()},
				ctrl, st, upd, nil)

			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/setup", nil)))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("Download root certificate"))
			Expect(body).To(ContainSubstring("/setup/wha-ca.mobileconfig"))
		})

		It("localizes the page title and body by default (German)", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/setup", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("<title>Sicherer Zugriff — wha</title>"))
			Expect(body).To(ContainSubstring("Sicherer Zugriff"))
			Expect(body).To(ContainSubstring("Installationsschritte pro Gerät"))
		})

		It("embeds translated markup as real HTML, not escaped text", func() {
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/setup", nil)))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("<code class=\"rounded bg-slate-100 px-1 py-0.5 text-xs dark:bg-slate-800\">tls internal</code>"))
			Expect(body).NotTo(ContainSubstring("&lt;code"))
		})

		It("localizes the settings page's /setup link label", func() {
			resp := doRequest(srv, enRequest(httptest.NewRequest(http.MethodGet, "/settings", nil)))
			Expect(bodyString(resp)).To(ContainSubstring("Secure access &amp; install as app"))

			deResp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/settings", nil))
			Expect(bodyString(deResp)).To(ContainSubstring("Sicherer Zugriff &amp; App-Installation"))
		})
	})

	Describe("GET /setup/root.crt", func() {
		It("returns a clean 404 when CARootPath is unset", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/setup/root.crt", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		})

		It("returns a clean 404 when CARootPath points at a missing file", func() {
			srv = web.New(config.Web{BindAddr: "127.0.0.1", Port: 0, CARootPath: "/nonexistent/root.crt"},
				ctrl, st, upd, nil)
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/setup/root.crt", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		})

		It("serves the configured certificate with the x509 content type", func() {
			certPath := writeTestCert()
			certBytes, err := os.ReadFile(certPath)
			Expect(err).NotTo(HaveOccurred())

			srv = web.New(config.Web{BindAddr: "127.0.0.1", Port: 0, CARootPath: certPath}, ctrl, st, upd, nil)
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/setup/root.crt", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("Content-Type")).To(Equal("application/x-x509-ca-cert"))
			Expect(bodyString(resp)).To(Equal(string(certBytes)))
		})
	})

	Describe("GET /setup/wha-ca.mobileconfig", func() {
		It("returns a clean 404 when CARootPath is unset", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/setup/wha-ca.mobileconfig", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		})

		It("returns a clean 404 when CARootPath points at a missing file", func() {
			srv = web.New(config.Web{BindAddr: "127.0.0.1", Port: 0, CARootPath: "/nonexistent/root.crt"},
				ctrl, st, upd, nil)
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/setup/wha-ca.mobileconfig", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		})

		It("serves an Apple configuration profile embedding the certificate", func() {
			srv = web.New(config.Web{BindAddr: "127.0.0.1", Port: 0, CARootPath: writeTestCert()},
				ctrl, st, upd, nil)
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/setup/wha-ca.mobileconfig", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("Content-Type")).To(Equal("application/x-apple-aspen-config"))

			body := bodyString(resp)
			Expect(body).To(ContainSubstring("<?xml version"))
			Expect(body).To(ContainSubstring("com.apple.security.root"))
			Expect(body).To(ContainSubstring("<data>"))
		})
	})
})
