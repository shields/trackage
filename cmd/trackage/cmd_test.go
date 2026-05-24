package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"msrl.dev/trackage"
)

// runRoot executes a fresh root command with the supplied args and
// returns the captured stdout, stderr, and exit code.
func runRoot(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var sout, serr bytes.Buffer
	code = realMain(args, &sout, &serr)
	return sout.String(), serr.String(), code
}

func TestDetectCmdMatch(t *testing.T) {
	t.Parallel()
	stdout, stderr, code := runRoot(t, "detect", "1Z999AA10123456784")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "ups") {
		t.Errorf("stdout = %q, want it to mention ups", stdout)
	}
}

func TestDetectCmdNoMatch(t *testing.T) {
	t.Parallel()
	stdout, _, code := runRoot(t, "detect", "weirdo")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout, "no match") {
		t.Errorf("stdout = %q, want 'no match'", stdout)
	}
}

func TestDetectCmdJSON(t *testing.T) {
	t.Parallel()
	stdout, _, code := runRoot(t, "detect", "--json", "1Z999AA10123456784")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal: %v\nstdout=%q", err, stdout)
	}
	if got["carrier"] != "ups" {
		t.Errorf("carrier = %q, want ups", got["carrier"])
	}
}

func TestCarriersCmd(t *testing.T) {
	t.Parallel()
	stdout, _, code := runRoot(t, "carriers")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout, "USPS") || !strings.Contains(stdout, "TRACKINGMORE") {
		t.Errorf("stdout = %q, want it to include the table", stdout)
	}
}

func TestCarriersCmdJSON(t *testing.T) {
	t.Parallel()
	stdout, _, code := runRoot(t, "carriers", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	var got []trackage.Carrier
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) < 10 {
		t.Errorf("got %d carriers, want at least 10", len(got))
	}
}

func TestBackendsCmd(t *testing.T) {
	t.Parallel()
	stdout, _, code := runRoot(t, "backends")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	for _, want := range []string{"Shippo", "EasyPost", "17Track", "TrackingMore", "KEY SOURCE"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q: %s", want, stdout)
		}
	}
}

func TestBackendsCmdJSON(t *testing.T) {
	t.Parallel()
	stdout, _, code := runRoot(t, "backends", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	var rows []backendRow
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 4 {
		t.Errorf("got %d rows, want 4", len(rows))
	}
	// Every row should carry the human-readable name.
	for _, r := range rows {
		if r.DisplayName == "" {
			t.Errorf("row %+v missing display_name", r)
		}
	}
}

func TestBackendsCmdKeySourceFromEnv(t *testing.T) {
	withDefaultCredsStore(t, "")
	t.Setenv("SHIPPO_API_KEY", "stub")
	stdout, _, code := runRootCfg(t, config{}, "backends")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	// Tabwriter pads columns, so we just want "Shippo … env … SHIPPO_API_KEY"
	// to appear in order on one line.
	if !strings.Contains(stdout, "env") {
		t.Errorf("expected env source in output, got %q", stdout)
	}
}

//nolint:paralleltest // mutates env + defaultCredsStore + execCommand seams
func TestBackendsCmdKeySourceFromKeychain(t *testing.T) {
	withDefaultCredsStore(t, "fake")
	withFakeHelper(t, "list-ok")
	// Make sure no env keys leak in.
	for _, env := range []string{"SHIPPO_API_KEY", "EASYPOST_API_KEY"} {
		t.Setenv(env, "")
	}
	stdout, _, code := runRootCfg(t, config{}, "backends", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	var rows []backendRow
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := make(map[string]string)
	for _, r := range rows {
		got[r.Name] = r.KeySource
	}
	if got["shippo"] != "keychain" || got["easypost"] != "keychain" {
		t.Errorf("expected shippo/easypost from keychain, got %v", got)
	}
	if got["17track"] != "" {
		t.Errorf("17track should not have a key (no entry in list), got %q", got["17track"])
	}
}

func TestBackendsCmdKeySourceFromConfig(t *testing.T) {
	withDefaultCredsStore(t, "")
	t.Setenv("SHIPPO_API_KEY", "")
	stdout, _, code := runRootCfg(t, config{
		APIKeys: map[string]string{"shippo": "from-config"},
	}, "backends", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	var rows []backendRow
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, r := range rows {
		if r.Name == "shippo" && r.KeySource != "config" {
			t.Errorf("shippo key_source = %q, want config", r.KeySource)
		}
	}
}

//nolint:paralleltest // mutates execCommand seam
func TestBackendsCmdListErrorWarnsAndProceeds(t *testing.T) {
	withFakeHelper(t, "list-error")
	for _, env := range []string{"SHIPPO_API_KEY", "EASYPOST_API_KEY", "SEVENTEENTRACK_API_KEY", "TRACKINGMORE_API_KEY"} {
		t.Setenv(env, "")
	}
	stdout, stderr, code := runRootCfg(t, config{CredsStore: "fake"}, "backends")
	if code != 0 {
		t.Fatalf("exit code = %d (the warning should not be fatal)", code)
	}
	if !strings.Contains(stderr, "warning:") {
		t.Errorf("expected warning on stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Shippo") {
		t.Errorf("table should still render with empty keychain info, got %q", stdout)
	}
}

func TestTrackCmdMissingBackend(t *testing.T) {
	t.Setenv("TRACKAGE_BACKEND", "")
	_, stderr, code := runRoot(t, "track", "x")
	if code == 0 {
		t.Fatal("expected non-zero exit for missing backend")
	}
	if !strings.Contains(stderr, "no backend selected") {
		t.Errorf("stderr = %q, want it to mention no backend", stderr)
	}
}

func TestTrackCmdUnknownBackend(t *testing.T) {
	t.Parallel()
	_, stderr, code := runRoot(t, "track", "--backend=bogus", "x")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "unknown backend") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestTrackCmdMissingAPIKey(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	withDefaultCredsStore(t, "")
	_, stderr, code := runRoot(t, "track", "--backend=shippo", "x")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "no API key") {
		t.Errorf("stderr = %q", stderr)
	}
}

// fakeTracker satisfies trackage.Tracker for CLI tests.
type fakeTracker struct {
	tracking *trackage.Tracking
	err      error
}

func (*fakeTracker) Name() string { return "fake" }
func (f *fakeTracker) Track(_ context.Context, _, _ string) (*trackage.Tracking, error) {
	return f.tracking, f.err
}

// withFakeBackend swaps backendRegistry to expose a single "fake"
// backend whose builder returns the supplied tracker. Returns a cleanup
// closure for defer.
func withFakeBackend(t *testing.T, ft *fakeTracker) {
	t.Helper()
	old := backendRegistry
	backendRegistry = []backendInfo{{
		Name:   "fake",
		EnvKey: "FAKE_API_KEY",
		Build:  func(string, *http.Client) trackage.Tracker { return ft },
	}}
	t.Cleanup(func() { backendRegistry = old })
	t.Setenv("FAKE_API_KEY", "key")
	t.Setenv("TRACKAGE_BACKEND", "fake")
}

//nolint:paralleltest // mutates package-level backendRegistry and env vars
func TestTrackCmdSuccessPretty(t *testing.T) {
	withFakeBackend(t, &fakeTracker{tracking: sampleTracking()})
	stdout, _, code := runRoot(t, "track", "1234")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout, "delivered") || !strings.Contains(stdout, "fake") {
		t.Errorf("stdout = %q", stdout)
	}
}

//nolint:paralleltest // mutates package-level backendRegistry and env vars
func TestTrackCmdSuccessJSON(t *testing.T) {
	withFakeBackend(t, &fakeTracker{tracking: sampleTracking()})
	stdout, _, code := runRoot(t, "track", "--json", "1234")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	var got trackage.Tracking
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal: %v\nstdout=%q", err, stdout)
	}
	if got.Status != trackage.StatusDelivered {
		t.Errorf("Status = %q", got.Status)
	}
}

//nolint:paralleltest // mutates package-level backendRegistry and env vars
func TestTrackCmdTrackerError(t *testing.T) {
	withFakeBackend(t, &fakeTracker{err: errors.New("upstream sad")})
	_, stderr, code := runRoot(t, "track", "1234")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "upstream sad") {
		t.Errorf("stderr = %q", stderr)
	}
}

func sampleTracking() *trackage.Tracking {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	eta := now.Add(24 * time.Hour)
	return &trackage.Tracking{
		Carrier:        trackage.CarrierUSPS,
		TrackingNumber: "9400111899223067387543",
		Status:         trackage.StatusDelivered,
		Substatus:      "delivered",
		Description:    "Delivered to mailbox.",
		LastUpdate:     now,
		EstDelivery:    &eta,
		Events: []trackage.Event{
			//nolint:lll // event fixture literal
			{Time: now.Add(-2 * time.Hour), Status: trackage.StatusInTransit, Description: "Out for delivery", Location: "City, ST"},
			{Time: now, Status: trackage.StatusDelivered, Description: "Delivered"},
		},
	}
}

func TestFriendlyBackendError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		in     error
		substr string
	}{
		{"not found", trackage.ErrNotFound, "tracking number not found"},
		{"auth", trackage.ErrAuth, "authentication failed"},
		{"rate limited", trackage.ErrRateLimited, "rate limited"},
		{"carrier required", trackage.ErrCarrierRequired, "carrier required"},
		{"unsupported", trackage.ErrUnsupportedCarrier, "carrier not supported"},
		{"opaque", errors.New("boom"), "fake: boom"},
	}
	for _, c := range cases {
		got := friendlyBackendError("fake", c.in)
		if !strings.Contains(got.Error(), c.substr) {
			t.Errorf("%s → %q, want substring %q", c.name, got, c.substr)
		}
		// Wrapping must preserve errors.Is for the sentinels.
		if c.in != nil && !errors.Is(got, c.in) {
			t.Errorf("%s: errors.Is broken", c.name)
		}
	}
}

func TestStatusLabel(t *testing.T) {
	t.Parallel()
	cases := map[trackage.Status]string{
		trackage.StatusPending:   "pending",
		trackage.StatusInTransit: "in transit",
		trackage.StatusDelivered: "delivered",
		trackage.StatusException: "exception",
		trackage.StatusUnknown:   "unknown",
		trackage.Status("weird"): "weird",
	}
	for in, want := range cases {
		if got := statusLabel(in); got != want {
			t.Errorf("statusLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatTime(t *testing.T) {
	t.Parallel()
	utc := time.Date(2026, 5, 21, 17, 53, 0, 0, time.UTC)
	if got := formatTime(utc); got != "2026-05-21 17:53 UTC" {
		t.Errorf("UTC time = %q, want 2026-05-21 17:53 UTC", got)
	}
	pdt := time.Date(2026, 5, 21, 4, 9, 0, 0, time.FixedZone("PDT", -7*3600))
	if got := formatTime(pdt); got != "2026-05-21 04:09 PDT" {
		t.Errorf("PDT time = %q, want 2026-05-21 04:09 PDT", got)
	}
	naive := time.Date(2026, 5, 21, 10, 53, 0, 0, time.FixedZone("local", 0))
	if got := formatTime(naive); got != "2026-05-21 10:53 local" {
		t.Errorf("zoneless sentinel = %q, want 2026-05-21 10:53 local", got)
	}
}

func TestWritePrettyMinimal(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := writePretty(&buf, "fake", &trackage.Tracking{
		TrackingNumber: "abc",
		Status:         trackage.StatusUnknown,
	}); err != nil {
		t.Fatalf("writePretty: %v", err)
	}
	if !strings.Contains(buf.String(), "abc") {
		t.Errorf("output missing tracking number: %s", buf.String())
	}
}

func TestWriteJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := writeJSON(&buf, &trackage.Tracking{TrackingNumber: "abc"}); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	var got trackage.Tracking
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.TrackingNumber != "abc" {
		t.Errorf("got %q", got.TrackingNumber)
	}
}

func TestOrDashHelpers(t *testing.T) {
	t.Parallel()
	if got := orDash(""); got != "-" {
		t.Errorf("orDash(empty) = %q, want -", got)
	}
	if got := orDash("usps"); got != "usps" {
		t.Errorf("orDash(usps) = %q", got)
	}
	if got := orDashInt(0); got != "-" {
		t.Errorf("orDashInt(0) = %q, want -", got)
	}
	if got := orDashInt(42); got != "42" {
		t.Errorf("orDashInt(42) = %q", got)
	}
}

func TestBackendByName(t *testing.T) {
	t.Parallel()
	if _, ok := backendByName("shippo"); !ok {
		t.Error("shippo should be a known backend")
	}
	if _, ok := backendByName("nope"); ok {
		t.Error("nope should not be a known backend")
	}
}

func TestResolveBackendFromEnv(t *testing.T) {
	t.Setenv("TRACKAGE_BACKEND", "shippo")
	t.Setenv("SHIPPO_API_KEY", "shippo_test_xyz")
	tr, name, err := resolveBackend(context.Background(), "", "", config{})
	if err != nil {
		t.Fatalf("resolveBackend: %v", err)
	}
	if name != "shippo" || tr.Name() != "shippo" {
		t.Errorf("name = %q, tr.Name = %q", name, tr.Name())
	}
}

// TestAllBackendBuildersWork exercises every backendRegistry.Build
// closure so the registry literal itself is covered.
func TestAllBackendBuildersWork(t *testing.T) {
	t.Parallel()
	for _, b := range backendRegistry {
		tr := b.Build("test-key", nil)
		if tr.Name() != b.Name {
			t.Errorf("%s.Name() = %q, want %q", b.Name, tr.Name(), b.Name)
		}
	}
}

// TestMainEntry exercises main()'s one-line body by swapping the
// osExit / osArgs indirection.
//
//nolint:paralleltest // mutates package-level osExit / osArgs
func TestMainEntry(t *testing.T) {
	origExit, origArgs := osExit, osArgs
	t.Cleanup(func() { osExit, osArgs = origExit, origArgs })

	var gotCode int
	osExit = func(code int) { gotCode = code }
	osArgs = []string{"trackage", "backends"}

	main()
	if gotCode != 0 {
		t.Errorf("exit code = %d, want 0", gotCode)
	}
}

func TestStringAndBoolFlag(t *testing.T) {
	t.Parallel()
	root := newRoot(config{})
	root.SetArgs([]string{"--json", "--backend=shippo", "carriers"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// boolFlag(json) returned true → JSON path → output is valid JSON.
	var rows []trackage.Carrier
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Errorf("expected JSON output: %v", err)
	}
}
