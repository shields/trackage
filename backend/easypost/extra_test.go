package easypost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"msrl.dev/trackage"
)

func TestName(t *testing.T) {
	t.Parallel()
	if got := (&Tracker{}).Name(); got != "easypost" {
		t.Errorf("Name = %q", got)
	}
}

func TestParseTime(t *testing.T) {
	t.Parallel()
	if got := parseTime("2025-05-09T20:40:17Z"); got.IsZero() {
		t.Error("RFC3339 should parse")
	}
	if got := parseTime("2025-05-12"); got.IsZero() {
		t.Error("date-only should parse")
	}
	if got := parseTime(""); !got.IsZero() {
		t.Error("empty should return zero")
	}
	if got := parseTime("garbage"); !got.IsZero() {
		t.Error("garbage should return zero")
	}
}

func TestParseErrorMaps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code int
		want error
	}{
		{http.StatusUnauthorized, trackage.ErrAuth},
		{http.StatusForbidden, trackage.ErrAuth},
		{http.StatusNotFound, trackage.ErrNotFound},
		{http.StatusTooManyRequests, trackage.ErrRateLimited},
		{http.StatusInternalServerError, nil},
	}
	for _, c := range cases {
		err := parseError(c.code, []byte(`{"error":{"code":"X","message":"y"}}`))
		if c.want == nil {
			continue
		}
		if !errors.Is(err, c.want) {
			t.Errorf("code %d → %v, want %v", c.code, err, c.want)
		}
	}
}

func TestParseErrorMalformedBody(t *testing.T) {
	t.Parallel()
	err := parseError(http.StatusBadRequest, []byte("not json"))
	var ae *trackage.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("expected APIError, got %T", err)
	}
	if !strings.Contains(ae.Message, "not json") {
		t.Errorf("Message should fall back to raw body: %q", ae.Message)
	}
}

func TestResolveCarrierVariants(t *testing.T) {
	t.Parallel()
	tr := &Tracker{}
	// Canonical ID we know: usps → "USPS".
	got, err := tr.resolveCarrier(trackage.CarrierUSPS, "x")
	if err != nil || got != "USPS" {
		t.Errorf("canonical USPS → %q, err=%v", got, err)
	}
	// Unknown string passes through.
	got, err = tr.resolveCarrier("strange", "x")
	if err != nil || got != "strange" {
		t.Errorf("passthrough → %q, err=%v", got, err)
	}
	// Detection from number works.
	got, err = tr.resolveCarrier("", "1Z999AA10123456784")
	if err != nil || got != "UPS" {
		t.Errorf("detected UPS → %q, err=%v", got, err)
	}
	// Detector misses → returns empty (let EasyPost auto-detect).
	got, err = tr.resolveCarrier("", "12")
	if err != nil || got != "" {
		t.Errorf("undetectable → %q, err=%v, want empty", got, err)
	}
}

func TestLocationString(t *testing.T) {
	t.Parallel()
	var l *trackingLocation
	if got := l.String(); got != "" {
		t.Errorf("nil → %q", got)
	}
	full := &trackingLocation{City: "C", State: "S", Country: "X", Zip: "Z"}
	if got := full.String(); got != "C, S, Z, X" {
		t.Errorf("full → %q", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	t.Parallel()
	if firstNonEmpty("", "", "x") != "x" {
		t.Error("should pick first non-empty")
	}
	if firstNonEmpty("", "") != "" {
		t.Error("all empty → empty")
	}
}

func TestNetworkErrorBubbles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil {
		t.Fatal("expected network error")
	}
}

func TestMalformedResponseBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, errors.New("read boom") }
func (errReadCloser) Close() error             { return nil }

type errBodyTransport struct{}

func (errBodyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: errReadCloser{}}, nil
}

func TestReadBodyError(t *testing.T) {
	t.Parallel()
	tr := New(Config{APIKey: "k", HTTPClient: &http.Client{Transport: errBodyTransport{}}})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil || !strings.Contains(err.Error(), "read boom") {
		t.Fatalf("expected read body error, got %v", err)
	}
}

func TestNewRequestError(t *testing.T) {
	t.Parallel()
	tr := New(Config{APIKey: "k", BaseURL: "http://example.com\x7f"})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil {
		t.Fatal("expected NewRequest error")
	}
}

//nolint:paralleltest // mutates package-level marshalJSON
func TestMarshalError(t *testing.T) {
	orig := marshalJSON
	t.Cleanup(func() { marshalJSON = orig })
	marshalJSON = func(any) ([]byte, error) { return nil, errors.New("marshal boom") }
	tr := New(Config{APIKey: "k"})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil || !strings.Contains(err.Error(), "marshal boom") {
		t.Fatalf("expected marshal error, got %v", err)
	}
}
