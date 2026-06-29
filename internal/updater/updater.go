// Package updater implements wha's in-UI software-update capability without
// giving wha any Docker access. wha runs in a distroless, non-root container and
// cannot replace its own image, so the actual pull-and-recreate is performed by
// a companion "wha-updater" sidecar that owns the Docker socket.
//
// This package is the wha-side half of that split:
//   - it checks GHCR for the newest published semver release (read-only,
//     anonymous registry token), and
//   - it signals the sidecar by writing a small request.json into a shared
//     directory, reading the sidecar's progress back from status.json.
//
// The target version is validated as a strict X.Y.Z semver before it is ever
// written, so it can only ever be consumed as an image tag — never as something
// the sidecar could interpret as a shell argument.
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
)

// defaultBaseURL is the GHCR host that serves both the anonymous token endpoint
// (/token) and the registry API (/v2). Overridable for tests.
const defaultBaseURL = "https://ghcr.io"

// requestFile and statusFile are the fixed filenames of the wha↔sidecar
// handshake inside the configured RequestDir.
const (
	requestFile = "request.json"
	statusFile  = "status.json"
)

// semverRe matches a bare X.Y.Z (no leading v, no pre-release). Registry tags
// are published without a leading "v" (GoReleaser .Version), and we only ever
// act on exact patch releases.
var semverRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// Info is the wha-side snapshot of update state shown in the UI.
type Info struct {
	// Enabled is false when the sidecar mechanism is not configured.
	Enabled bool `json:"enabled"`
	// Current is the running wha version (the build's ldflags version).
	Current string `json:"current"`
	// Latest is the newest semver tag found on GHCR; empty until a check runs.
	Latest string `json:"latest"`
	// UpdateAvailable is true when Latest is a strictly newer semver than Current.
	UpdateAvailable bool `json:"updateAvailable"`
	// CheckedAt is when Latest was last refreshed from GHCR; zero if never.
	CheckedAt time.Time `json:"checkedAt"`

	// State/Message/TargetVersion mirror the sidecar's status.json so the UI can
	// reflect an in-flight or just-finished update.
	State         string `json:"state"`
	Message       string `json:"message"`
	TargetVersion string `json:"targetVersion"`
}

// request is the wha→sidecar message: please update to TargetVersion.
type request struct {
	TargetVersion string    `json:"targetVersion"`
	RequestedAt   time.Time `json:"requestedAt"`
}

// status is the sidecar→wha message read back from status.json. State is one of
// idle|applying|done|failed.
type status struct {
	State         string    `json:"state"`
	TargetVersion string    `json:"targetVersion"`
	Message       string    `json:"message"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Updater checks GHCR and drives the sidecar handshake. It is safe for
// concurrent use.
type Updater struct {
	cfg     config.Update
	current string
	hc      *http.Client
	baseURL string
	now     func() time.Time

	mu      sync.Mutex
	latest  string
	checked time.Time
}

// Option customizes an Updater (used by tests to inject a clock, HTTP client,
// and a fake GHCR base URL).
type Option func(*Updater)

// WithHTTPClient sets the HTTP client used for GHCR requests.
func WithHTTPClient(hc *http.Client) Option { return func(u *Updater) { u.hc = hc } }

// WithClock sets the time source.
func WithClock(now func() time.Time) Option { return func(u *Updater) { u.now = now } }

// WithBaseURL overrides the GHCR base URL (token + registry endpoints).
func WithBaseURL(url string) Option {
	return func(u *Updater) { u.baseURL = strings.TrimRight(url, "/") }
}

// New builds an Updater for the given config and running version.
func New(cfg config.Update, current string, opts ...Option) *Updater {
	u := &Updater{
		cfg:     cfg,
		current: current,
		hc:      &http.Client{Timeout: 10 * time.Second},
		baseURL: defaultBaseURL,
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(u)
	}
	return u
}

// Info returns the current update snapshot. It is cheap enough to call on every
// dashboard poll: it reads the small local status.json but never hits the
// network (use Check for that).
func (u *Updater) Info(_ context.Context) Info {
	u.mu.Lock()
	latest, checked := u.latest, u.checked
	u.mu.Unlock()

	info := Info{
		Enabled:         u.cfg.Enabled,
		Current:         u.current,
		Latest:          latest,
		UpdateAvailable: newer(u.current, latest),
		CheckedAt:       checked,
	}
	if st, ok := u.readStatus(); ok {
		info.State = st.State
		info.Message = st.Message
		info.TargetVersion = st.TargetVersion
	}
	return info
}

// Check refreshes Latest from GHCR and returns the updated snapshot. To avoid
// hammering the registry (e.g. an operator spamming the "Check" button), a
// result younger than CheckTTL is reused instead of re-querying. A CheckTTL of
// 0 disables the throttle and always hits the network.
func (u *Updater) Check(ctx context.Context) (Info, error) {
	u.mu.Lock()
	fresh := !u.checked.IsZero() && u.cfg.CheckTTL > 0 && u.now().Sub(u.checked) < u.cfg.CheckTTL
	u.mu.Unlock()
	if fresh {
		return u.Info(ctx), nil
	}

	latest, err := latestSemver(ctx, u.hc, u.baseURL, u.cfg.Repository)
	if err != nil {
		return u.Info(ctx), fmt.Errorf("check for updates: %w", err)
	}
	u.mu.Lock()
	u.latest = latest
	u.checked = u.now()
	u.mu.Unlock()
	return u.Info(ctx), nil
}

// Trigger validates the requested version and writes the request file the
// sidecar polls for. The version must be a strict X.Y.Z semver and differ from
// the running version; callers should additionally constrain it to a version
// GHCR actually offers (see Info.Latest).
func (u *Updater) Trigger(_ context.Context, version string) error {
	if !u.cfg.Enabled {
		return fmt.Errorf("updates are disabled")
	}
	if !semverRe.MatchString(version) {
		return fmt.Errorf("invalid target version %q (want X.Y.Z)", version)
	}
	if version == u.current {
		return fmt.Errorf("already running version %s", version)
	}

	if err := os.MkdirAll(u.cfg.RequestDir, 0o755); err != nil {
		return fmt.Errorf("create request dir: %w", err)
	}
	body, err := json.Marshal(request{TargetVersion: version, RequestedAt: u.now().UTC()})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	return writeFileAtomic(filepath.Join(u.cfg.RequestDir, requestFile), body)
}

// readStatus reads the sidecar's status.json. A missing file is normal (no
// update has run) and reported as ok=false.
func (u *Updater) readStatus() (status, bool) {
	if u.cfg.RequestDir == "" {
		return status{}, false
	}
	b, err := os.ReadFile(filepath.Join(u.cfg.RequestDir, statusFile))
	if err != nil {
		return status{}, false
	}
	var st status
	if err := json.Unmarshal(b, &st); err != nil {
		return status{}, false
	}
	return st, true
}

// writeFileAtomic writes data to path via a temp file + rename so a reader (the
// sidecar) never observes a partially-written request.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// latestSemver fetches an anonymous pull token, lists the repository tags, and
// returns the highest X.Y.Z tag. Non-semver tags (latest, edge, sha…) are
// ignored. Mirrors the GHCR flow used by scripts/install.sh.
func latestSemver(ctx context.Context, hc *http.Client, baseURL, repo string) (string, error) {
	token, err := fetchPullToken(ctx, hc, baseURL, repo)
	if err != nil {
		return "", err
	}
	tags, err := fetchTags(ctx, hc, baseURL, repo, token)
	if err != nil {
		return "", err
	}
	return maxSemver(tags), nil
}

func fetchPullToken(ctx context.Context, hc *http.Client, baseURL, repo string) (string, error) {
	url := fmt.Sprintf("%s/token?scope=repository:%s:pull&service=ghcr.io", baseURL, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %s", resp.Status)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	if body.Token == "" {
		return "", fmt.Errorf("empty pull token")
	}
	return body.Token, nil
}

// maxTagPages bounds the pagination loop so a misbehaving registry can never
// hold the request open indefinitely. 100 pages × 100 tags/page is far beyond
// any realistic tag count for this repo.
const maxTagPages = 100

// fetchTags lists every tag for repo, following the registry's RFC 5988
// `Link: <url>; rel="next"` pagination so a newer semver on a later page is
// never missed (GHCR defaults to 100 tags per page).
func fetchTags(ctx context.Context, hc *http.Client, baseURL, repo, token string) ([]string, error) {
	next := fmt.Sprintf("%s/v2/%s/tags/list", baseURL, repo)
	var all []string
	for page := 0; next != "" && page < maxTagPages; page++ {
		tags, link, err := fetchTagsPage(ctx, hc, next, token)
		if err != nil {
			return nil, err
		}
		all = append(all, tags...)
		next = nextLink(link, baseURL)
	}
	return all, nil
}

// fetchTagsPage fetches a single tags page and returns its tags plus the raw
// Link response header (empty when there is no further page).
func fetchTagsPage(ctx context.Context, hc *http.Client, url, token string) ([]string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build tags request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("request tags: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("tags endpoint returned %s", resp.Status)
	}
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, "", fmt.Errorf("decode tags: %w", err)
	}
	return body.Tags, resp.Header.Get("Link"), nil
}

// nextLink extracts the rel="next" target from a Link header, resolving a
// registry-relative path (e.g. "/v2/...") against baseURL. It returns "" when
// there is no next page.
func nextLink(header, baseURL string) string {
	for part := range strings.SplitSeq(header, ",") {
		segs := strings.Split(part, ";")
		if len(segs) < 2 {
			continue
		}
		urlPart := strings.TrimSpace(segs[0])
		if !strings.HasPrefix(urlPart, "<") || !strings.HasSuffix(urlPart, ">") {
			continue
		}
		isNext := false
		for _, p := range segs[1:] {
			if strings.ReplaceAll(strings.TrimSpace(p), `"`, "") == "rel=next" {
				isNext = true
			}
		}
		if !isNext {
			continue
		}
		u := urlPart[1 : len(urlPart)-1]
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			return u
		}
		return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(u, "/")
	}
	return ""
}

// maxSemver returns the highest X.Y.Z tag in tags, or "" if there are none.
func maxSemver(tags []string) string {
	semver := make([]string, 0, len(tags))
	for _, t := range tags {
		if semverRe.MatchString(t) {
			semver = append(semver, t)
		}
	}
	if len(semver) == 0 {
		return ""
	}
	sort.Slice(semver, func(i, j int) bool { return compareSemver(semver[i], semver[j]) > 0 })
	return semver[0]
}

// newer reports whether latest is a strictly higher semver than current. A
// non-semver current (e.g. a "dev" build) or empty latest yields false.
func newer(current, latest string) bool {
	if !semverRe.MatchString(current) || !semverRe.MatchString(latest) {
		return false
	}
	return compareSemver(latest, current) > 0
}

// compareSemver returns >0 if a>b, <0 if a<b, 0 if equal. Inputs must already be
// valid X.Y.Z strings.
func compareSemver(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range 3 {
		na, _ := strconv.Atoi(pa[i])
		nb, _ := strconv.Atoi(pb[i])
		if na != nb {
			if na > nb {
				return 1
			}
			return -1
		}
	}
	return 0
}
