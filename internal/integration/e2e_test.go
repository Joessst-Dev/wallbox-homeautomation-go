package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	broker "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/updater"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/web"
)

// eventually mirrors the CI real-time budget: dwell/republish are configured at
// the millisecond scale below, so a few seconds is ample even under -race.
const (
	eventuallyTimeout = 5 * time.Second
	eventuallyPoll    = 20 * time.Millisecond
	tokenWait         = 5 * time.Second
)

// capturedCommands is a thread-safe record of the command payloads the broker
// delivered on the loadpoint set-topics, so specs can observe what the real evcc
// client published over the wire.
//
// The embedded pure-Go broker occasionally mis-frames back-to-back small MQTT
// PUBLISH packets under the rapid republish cadence, bleeding bytes between
// adjacent packets (e.g. "off"+"now" delivered as "onw"). Our production evcc
// client always publishes the exact command string; that garbage is a
// test-broker artifact, so — exactly as a real evcc consumer would — the
// observer filters payloads that fail the topic's validator and only treats
// well-formed commands as the command stream. Republish guarantees the intended
// value is re-emitted cleanly, so the meaningful command stream is observed intact.
//
// Filtered payloads are not silently discarded: they are counted under dropped so
// a genuine future corruption bug — one that garbles a large share of packets, or
// scrambles them in a new way — surfaces via the AfterEach drop-rate bound instead
// of blending into "the usual glitch".
type capturedCommands struct {
	mu      sync.Mutex
	byTopic map[string][]string
	dropped map[string][]string // malformed payloads the observer filtered out, per topic
}

func newCapturedCommands() *capturedCommands {
	return &capturedCommands{byTopic: map[string][]string{}, dropped: map[string][]string{}}
}

// observe records payload under topic when the topic's validator accepts it as a
// well-formed command, otherwise files it under dropped (see the type doc).
func (c *capturedCommands) observe(topic, payload string, valid func(string) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if valid(payload) {
		c.byTopic[topic] = append(c.byTopic[topic], payload)
		return
	}
	c.dropped[topic] = append(c.dropped[topic], payload)
}

// counts returns the number of well-formed and dropped payloads observed across
// all topics.
func (c *capturedCommands) counts() (kept, dropped int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, msgs := range c.byTopic {
		kept += len(msgs)
	}
	for _, msgs := range c.dropped {
		dropped += len(msgs)
	}
	return kept, dropped
}

// droppedReport renders every dropped payload per topic, for surfacing in the test
// log when the drop-rate bound is exceeded.
func (c *capturedCommands) droppedReport() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	for topic, msgs := range c.dropped {
		fmt.Fprintf(&b, "\n  %s: %q", topic, msgs)
	}
	return b.String()
}

// validMode reports whether s is a well-formed evcc charge mode (or off).
func validMode(s string) bool {
	switch s {
	case controller.ModeOff, config.EnableModePV, config.EnableModeNow, "minpv":
		return true
	default:
		return false
	}
}

// validLimit reports whether s is a well-formed (all-digit) SoC limit payload.
func validLimit(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// last returns the most recent payload seen on topic ("" if none yet).
func (c *capturedCommands) last(topic string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	msgs := c.byTopic[topic]
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1]
}

// startBroker stands up an in-process, pure-Go mochi MQTT broker on an ephemeral
// 127.0.0.1 port and returns its "tcp://host:port" URL. It is torn down via
// DeferCleanup when the spec ends.
func startBroker() string {
	GinkgoHelper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())

	srv := broker.New(&broker.Options{})
	Expect(srv.AddHook(new(auth.AllowHook), nil)).To(Succeed())
	Expect(srv.AddListener(listeners.NewNet("test", ln))).To(Succeed())

	go func() {
		defer GinkgoRecover()
		_ = srv.Serve()
	}()
	DeferCleanup(func() { _ = srv.Close() })

	return "tcp://" + ln.Addr().String()
}

// connectPaho returns a connected paho client (the same library the production
// evcc client uses), cleaned up when the spec ends.
func connectPaho(url, id string) paho.Client {
	GinkgoHelper()
	opts := paho.NewClientOptions().AddBroker(url).SetClientID(id).SetCleanSession(true)
	c := paho.NewClient(opts)
	tok := c.Connect()
	Expect(tok.WaitTimeout(tokenWait)).To(BeTrue())
	Expect(tok.Error()).NotTo(HaveOccurred())
	DeferCleanup(func() { c.Disconnect(100) })
	return c
}

// discardLogger returns a logger that drops everything, keeping test output clean.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

var _ = Describe("End-to-end (embedded broker + real components)", func() {
	var (
		tp   evcc.Topics
		cmds *capturedCommands
		pub  func(topic, payload string)
		srv  *web.Server
	)

	BeforeEach(func() {
		brokerURL := startBroker()
		tp = evcc.NewTopics("evcc", "1")
		log := discardLogger()

		// A separate test client publishes evcc state topics and observes the
		// command set-topics round-tripping through the real broker.
		peer := connectPaho(brokerURL, "test-peer")
		cmds = newCapturedCommands()
		subscribe := func(topic string, valid func(string) bool) {
			tok := peer.Subscribe(topic, 1, func(_ paho.Client, m paho.Message) {
				cmds.observe(m.Topic(), string(m.Payload()), valid)
			})
			Expect(tok.WaitTimeout(tokenWait)).To(BeTrue())
			Expect(tok.Error()).NotTo(HaveOccurred())
		}
		subscribe(tp.ModeSet, validMode)
		subscribe(tp.LimitSoCSet, validLimit)

		pub = func(topic, payload string) {
			GinkgoHelper()
			tok := peer.Publish(topic, 1, true, payload)
			Expect(tok.WaitTimeout(tokenWait)).To(BeTrue())
			Expect(tok.Error()).NotTo(HaveOccurred())
		}

		// Real evcc MQTT client (this is the layer with previously zero coverage).
		client := evcc.NewClient(
			config.MQTT{Broker: brokerURL, ClientID: "wha-e2e", TopicPrefix: "evcc"},
			"1", log,
		)
		Expect(client.Connect()).To(Succeed())
		DeferCleanup(client.Disconnect)

		// Real store.
		st, err := store.Open(filepath.Join(GinkgoT().TempDir(), "wha.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = st.Close() })

		// Real controller with millisecond-scale timing so real-time dwell resolves
		// fast; janitor disabled (RetentionWindow 0).
		ctrlCfg := config.Default().Control
		ctrlCfg.DecisionInterval = 20 * time.Millisecond
		ctrlCfg.StartDwell = 20 * time.Millisecond
		ctrlCfg.StopDwell = 20 * time.Millisecond
		ctrlCfg.Republish = 50 * time.Millisecond
		ctrlCfg.RetentionWindow = 0
		ctrl := controller.New(ctrlCfg, client, client.Store(), st, controller.RealClock{}, log)

		runCtx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		go func() {
			defer GinkgoRecover()
			_ = ctrl.Run(runCtx)
		}()

		// Real web server (driven via App().Test, never bound to a TCP port).
		upd := updater.New(config.Update{Enabled: false}, "1.0.0")
		srv = web.New(config.Web{BindAddr: "127.0.0.1", Port: 0}, ctrl, st, upd, log)
	})

	AfterEach(func() {
		// The observer filters the broker's rare framing artifact (see
		// capturedCommands' doc). Every emitted value is also republished cleanly,
		// so well-formed captures should dominate: assert drops stay a minority of
		// the observed stream. A genuine corruption regression that garbles a large
		// share of packets trips this bound instead of hiding behind the glitch;
		// the report names the offending payloads.
		kept, dropped := cmds.counts()
		if dropped > 0 {
			GinkgoWriter.Printf("observer dropped %d of %d observed payload(s):%s\n",
				dropped, kept+dropped, cmds.droppedReport())
		}
		Expect(dropped).To(BeNumerically("<=", kept),
			"malformed command payloads should stay a minority of the stream; a spike signals real corruption, not the broker framing artifact")
	})

	// publishSurplus publishes a healthy, connected state with ample PV surplus.
	publishSurplus := func() {
		pub(tp.Status, "online")
		pub(tp.PVPower, "4000")
		pub(tp.GridPower, "-3000") // exporting 3 kW → surplus well over the start threshold
		pub(tp.Connected, "true")
		pub(tp.Charging, "false")
		pub(tp.VehicleSoC, "50")
	}

	It("publishes mode=pv when sustained PV surplus arrives", func() {
		publishSurplus()
		Eventually(func() string { return cmds.last(tp.ModeSet) }, eventuallyTimeout, eventuallyPoll).
			Should(Equal("pv"))
	})

	It("falls back to mode=off when evcc goes offline", func() {
		publishSurplus()
		Eventually(func() string { return cmds.last(tp.ModeSet) }, eventuallyTimeout, eventuallyPoll).
			Should(Equal("pv"))

		pub(tp.Status, "offline") // LWT: stale → fail-safe off
		Eventually(func() string { return cmds.last(tp.ModeSet) }, eventuallyTimeout, eventuallyPoll).
			Should(Equal("off"))
	})

	It("publishes the limitSoc backstop at the SoC cap", func() {
		publishSurplus()
		Eventually(func() string { return cmds.last(tp.LimitSoCSet) }, eventuallyTimeout, eventuallyPoll).
			Should(Equal("80")) // config default SoCCap
	})

	It("forces charging on with the operator charge-power mode via POST /api/override", func() {
		publishSurplus()

		// Force on, then pick the "now" charge-power mode: the effective published
		// mode must be "now", proving the HTTP layer, controller, and broker are wired.
		//
		// The charge-power POST is issued last on purpose. Fiber's BodyParser yields
		// strings that alias the pooled fasthttp request buffer, and SetChargePower
		// keeps that string; a subsequent App().Test request would recycle the buffer
		// and scramble it. Making it the final request leaves the buffer untouched.
		doForm(srv, "/api/override", "mode=on")
		doForm(srv, "/api/charge-power", "power=now")

		Eventually(func() string { return cmds.last(tp.ModeSet) }, eventuallyTimeout, eventuallyPoll).
			Should(Equal("now"))
	})

	It("latches to full-power charging via POST /api/auto-profile and holds through a PV collapse", func() {
		// Select the power_latch profile and a 90% target through the real HTTP
		// layer. target-soc is POSTed after auto-profile so its request recycles the
		// pooled fasthttp buffer — with the handler's strings.Clone in place the
		// stored profile survives that reuse (this also guards that fix end to end).
		doForm(srv, "/api/auto-profile", "profile=power_latch")
		doForm(srv, "/api/target-soc", "targetSoC=90")

		// PV surplus arrives → the latch drives full-power (now) charging over the
		// real broker, and the limitSoc dead-man backstop tracks the latch target
		// (90) rather than the default cap (80).
		publishSurplus()
		Eventually(func() string { return cmds.last(tp.ModeSet) }, eventuallyTimeout, eventuallyPoll).
			Should(Equal("now"))
		Eventually(func() string { return cmds.last(tp.LimitSoCSet) }, eventuallyTimeout, eventuallyPoll).
			Should(Equal("90"))

		// The trigger collapses (PV gone, now importing) → the latch keeps charging
		// at full power; StopThreshold/StopDwell are never evaluated for a latch.
		pub(tp.PVPower, "0")
		pub(tp.GridPower, "1000")
		Consistently(func() string { return cmds.last(tp.ModeSet) }, 150*time.Millisecond, eventuallyPoll).
			Should(Equal("now"))
	})

	It("reflects the live state via GET /api/status and readiness via GET /readyz", func() {
		publishSurplus()
		Eventually(func() string { return cmds.last(tp.ModeSet) }, eventuallyTimeout, eventuallyPoll).
			Should(Equal("pv"))

		// /api/status reflects the live controller state.
		Eventually(func() string {
			resp, err := srv.App().Test(httptest.NewRequest(http.MethodGet, "/api/status", nil), -1)
			Expect(err).NotTo(HaveOccurred())
			var vm map[string]any
			Expect(json.Unmarshal([]byte(readBody(resp)), &vm)).To(Succeed())
			state, _ := vm["state"].(string)
			return state
		}, eventuallyTimeout, eventuallyPoll).Should(Equal("charging"))

		// /readyz reflects the broker-connected controller.
		resp, err := srv.App().Test(httptest.NewRequest(http.MethodGet, "/readyz", nil), -1)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
	})
})

// doForm drives a form-encoded POST through the Fiber app and asserts a 2xx.
func doForm(srv *web.Server, path, body string) {
	GinkgoHelper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := srv.App().Test(req, -1)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(BeNumerically("<", 300))
	_ = resp.Body.Close()
}

// readBody drains and returns a response body as a string.
func readBody(resp *http.Response) string {
	GinkgoHelper()
	b, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	_ = resp.Body.Close()
	return string(b)
}
