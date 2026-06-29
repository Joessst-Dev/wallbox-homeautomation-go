package updater_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/updater"
)

const repo = "joessst-dev/wha"

// fakeGHCR stands in for the GHCR token + tags endpoints. tags is the list it
// reports; failToken/failTags force error responses.
func fakeGHCR(tags []string, failToken, failTags bool) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		if failToken {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "t0ken"})
	})
	mux.HandleFunc("/v2/"+repo+"/tags/list", func(w http.ResponseWriter, r *http.Request) {
		if failTags {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		Expect(r.Header.Get("Authorization")).To(Equal("Bearer t0ken"))
		_ = json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": tags})
	})
	return httptest.NewServer(mux)
}

// fakeGHCRCounting behaves like fakeGHCR but records how many times the tags
// endpoint is hit, so specs can assert the CheckTTL throttle.
func fakeGHCRCounting(tags []string, calls *int) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "t0ken"})
	})
	mux.HandleFunc("/v2/"+repo+"/tags/list", func(w http.ResponseWriter, _ *http.Request) {
		*calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": tags})
	})
	return httptest.NewServer(mux)
}

// fakeGHCRPaged serves the tags across two pages, linking page 1 to page 2 with
// a relative rel="next" Link header so pagination + baseURL resolution are
// exercised.
func fakeGHCRPaged(page1, page2 []string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "t0ken"})
	})
	mux.HandleFunc("/v2/"+repo+"/tags/list", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" {
			w.Header().Set("Link", `</v2/`+repo+`/tags/list?page=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": page1})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": page2})
	})
	return httptest.NewServer(mux)
}

var _ = Describe("Updater", func() {
	var (
		ctx   context.Context
		clock time.Time
		cfg   config.Update
	)

	BeforeEach(func() {
		ctx = context.Background()
		clock = time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
		cfg = config.Update{
			Enabled:    true,
			Repository: repo,
			RequestDir: GinkgoT().TempDir(),
			CheckTTL:   time.Hour,
		}
	})

	newUpdater := func(current, baseURL string) *updater.Updater {
		return updater.New(cfg, current,
			updater.WithBaseURL(baseURL),
			updater.WithClock(func() time.Time { return clock }),
		)
	}

	Describe("Check", func() {
		It("picks the newest semver and ignores non-semver tags", func() {
			srv := fakeGHCR([]string{"edge", "latest", "1.2.3", "1.10.0", "1.9.0", "sha123"}, false, false)
			defer srv.Close()

			info, err := newUpdater("1.2.3", srv.URL).Check(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Latest).To(Equal("1.10.0"))
			Expect(info.UpdateAvailable).To(BeTrue())
			Expect(info.CheckedAt).To(BeTemporally("==", clock))
		})

		It("reports no update when already on the newest", func() {
			srv := fakeGHCR([]string{"1.2.3", "latest"}, false, false)
			defer srv.Close()

			info, err := newUpdater("1.2.3", srv.URL).Check(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Latest).To(Equal("1.2.3"))
			Expect(info.UpdateAvailable).To(BeFalse())
		})

		It("never reports an update for a non-semver dev build", func() {
			srv := fakeGHCR([]string{"1.2.3"}, false, false)
			defer srv.Close()

			info, err := newUpdater("dev", srv.URL).Check(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Latest).To(Equal("1.2.3"))
			Expect(info.UpdateAvailable).To(BeFalse())
		})

		It("returns an error when the registry is unreachable", func() {
			srv := fakeGHCR(nil, true, false)
			defer srv.Close()

			_, err := newUpdater("1.2.3", srv.URL).Check(ctx)
			Expect(err).To(HaveOccurred())
		})

		It("follows Link headers to find a newer semver on a later page", func() {
			srv := fakeGHCRPaged([]string{"1.2.3", "edge"}, []string{"1.4.0", "latest"})
			defer srv.Close()

			info, err := newUpdater("1.2.3", srv.URL).Check(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Latest).To(Equal("1.4.0"))
			Expect(info.UpdateAvailable).To(BeTrue())
		})

		It("reuses a result within CheckTTL instead of re-querying", func() {
			var calls int
			srv := fakeGHCRCounting([]string{"1.3.0"}, &calls)
			defer srv.Close()

			u := newUpdater("1.2.3", srv.URL)
			_, err := u.Check(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(calls).To(Equal(1))

			// Second check within the TTL is served from cache.
			clock = clock.Add(30 * time.Minute)
			_, err = u.Check(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(calls).To(Equal(1))

			// Past the TTL it queries again.
			clock = clock.Add(31 * time.Minute)
			_, err = u.Check(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(calls).To(Equal(2))
		})
	})

	Describe("Trigger", func() {
		requestPath := func() string { return filepath.Join(cfg.RequestDir, "request.json") }

		It("writes a well-formed request file for a valid semver", func() {
			u := newUpdater("1.2.3", "http://unused")
			Expect(u.Trigger(ctx, "1.3.0")).To(Succeed())

			b, err := os.ReadFile(requestPath())
			Expect(err).NotTo(HaveOccurred())
			var req struct {
				TargetVersion string    `json:"targetVersion"`
				RequestedAt   time.Time `json:"requestedAt"`
			}
			Expect(json.Unmarshal(b, &req)).To(Succeed())
			Expect(req.TargetVersion).To(Equal("1.3.0"))
			Expect(req.RequestedAt).To(BeTemporally("==", clock))
		})

		It("rejects a non-semver target and writes nothing (injection guard)", func() {
			u := newUpdater("1.2.3", "http://unused")
			Expect(u.Trigger(ctx, "1.3.0; rm -rf /")).To(MatchError(ContainSubstring("invalid target version")))
			Expect(u.Trigger(ctx, "latest")).To(MatchError(ContainSubstring("invalid target version")))
			_, err := os.Stat(requestPath())
			Expect(os.IsNotExist(err)).To(BeTrue())
		})

		It("rejects the currently-running version", func() {
			u := newUpdater("1.2.3", "http://unused")
			Expect(u.Trigger(ctx, "1.2.3")).To(MatchError(ContainSubstring("already running")))
		})

		It("refuses when updates are disabled", func() {
			cfg.Enabled = false
			u := newUpdater("1.2.3", "http://unused")
			Expect(u.Trigger(ctx, "1.3.0")).To(MatchError(ContainSubstring("disabled")))
		})
	})

	Describe("Info", func() {
		It("reflects the sidecar's status.json", func() {
			st := map[string]any{
				"state":         "applying",
				"targetVersion": "1.3.0",
				"message":       "pulling image",
				"updatedAt":     clock,
			}
			b, err := json.Marshal(st)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(cfg.RequestDir, "status.json"), b, 0o644)).To(Succeed())

			info := newUpdater("1.2.3", "http://unused").Info(ctx)
			Expect(info.Enabled).To(BeTrue())
			Expect(info.Current).To(Equal("1.2.3"))
			Expect(info.State).To(Equal("applying"))
			Expect(info.TargetVersion).To(Equal("1.3.0"))
			Expect(info.Message).To(Equal("pulling image"))
		})

		It("is safe when no status file exists yet", func() {
			info := newUpdater("1.2.3", "http://unused").Info(ctx)
			Expect(info.State).To(BeEmpty())
			Expect(info.Current).To(Equal("1.2.3"))
		})
	})
})
