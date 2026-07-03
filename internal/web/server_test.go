package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

// fakeController is a deterministic stand-in for *controller.Controller that
// records the last SetOverride call so specs can assert on it.
type fakeController struct {
	status controller.StatusView

	overrideCalled    bool
	overrideMode      controller.Override
	overrideUntil     time.Time
	overrideCapBypass bool

	chargePowerCalled bool
	chargePowerMode   string
	chargePowerErr    error
}

func (f *fakeController) Status() controller.StatusView { return f.status }

func (f *fakeController) SetOverride(mode controller.Override, until time.Time, capBypass bool) {
	f.overrideCalled = true
	f.overrideMode = mode
	f.overrideUntil = until
	f.overrideCapBypass = capBypass
}

func (f *fakeController) SetChargePower(mode string) error {
	f.chargePowerCalled = true
	f.chargePowerMode = mode
	return f.chargePowerErr
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
	})

	Describe("GET /", func() {
		It("renders the dashboard HTML with key labels", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).To(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring("Surplus"))
			Expect(body).To(ContainSubstring("Vehicle SoC"))
			Expect(body).To(ContainSubstring("Override"))
			Expect(body).To(ContainSubstring(`hx-get="/partials/status"`))
		})
	})

	Describe("GET /partials/status", func() {
		It("renders the status fragment without the layout", func() {
			resp := doRequest(srv, httptest.NewRequest(http.MethodGet, "/partials/status", nil))
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).NotTo(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring("Surplus"))
			Expect(body).To(ContainSubstring("charging"))
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
			It("returns the refreshed status partial", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/override",
					strings.NewReader("mode=off"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("HX-Request", "true")

				resp := doRequest(srv, req)
				Expect(resp.StatusCode).To(Equal(http.StatusOK))
				Expect(ctrl.overrideMode).To(Equal(controller.OverrideForceOff))
				body := bodyString(resp)
				Expect(body).NotTo(ContainSubstring("<!DOCTYPE html>"))
				Expect(body).To(ContainSubstring("Surplus"))
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

		It("returns the refreshed status partial for htmx requests", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/charge-power",
				strings.NewReader("power=pv"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("HX-Request", "true")

			resp := doRequest(srv, req)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := bodyString(resp)
			Expect(body).NotTo(ContainSubstring("<!DOCTYPE html>"))
			Expect(body).To(ContainSubstring("Charge power"))
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
	})
})
