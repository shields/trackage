package trackingmore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"msrl.dev/trackage"
)

func TestName(t *testing.T) {
	t.Parallel()
	if got := (&Tracker{}).Name(); got != "trackingmore" {
		t.Errorf("Name = %q", got)
	}
}

func TestResolveCarrierVariants(t *testing.T) {
	t.Parallel()
	tr := &Tracker{}
	got, err := tr.resolveCarrier(trackage.CarrierDHLExpress, "x")
	if err != nil || got != "dhl" {
		t.Errorf("canonical → %q, err=%v", got, err)
	}
	got, err = tr.resolveCarrier("custom-slug", "x")
	if err != nil || got != "custom-slug" {
		t.Errorf("passthrough → %q, err=%v", got, err)
	}
	got, err = tr.resolveCarrier("", "1Z999AA10123456784")
	if err != nil || got != "ups" {
		t.Errorf("detected → %q, err=%v", got, err)
	}
	got, err = tr.resolveCarrier("", "12")
	if err != nil || got != "" {
		t.Errorf("undetectable → %q, err=%v", got, err)
	}
}

func TestEnvAsErrorMaps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		meta int
		want error
	}{
		{4001, trackage.ErrAuth},
		{4002, trackage.ErrAuth},
		{http.StatusUnauthorized, trackage.ErrAuth},
		{http.StatusTooManyRequests, trackage.ErrRateLimited},
		{http.StatusNotFound, trackage.ErrNotFound},
		{500, nil},
	}
	for _, c := range cases {
		env := envelope{Meta: meta{Code: c.meta, Message: "x"}}
		err := envAsError(c.meta, env)
		if c.want == nil {
			continue
		}
		if !errors.Is(err, c.want) {
			t.Errorf("meta %d → %v, want %v", c.meta, err, c.want)
		}
	}
}

func TestParseTime(t *testing.T) {
	t.Parallel()
	if got := parseTime(""); !got.IsZero() {
		t.Error("empty → zero")
	}
	if got := parseTime("garbage"); !got.IsZero() {
		t.Error("garbage → zero")
	}
	if got := parseTime("2015-10-30T11:35:16+08:00"); got.IsZero() {
		t.Error("ISO with offset should parse")
	}
	if got := parseTime("2015-11-02 17:11:00"); got.IsZero() {
		t.Error("space-separated full timestamp should parse")
	}
	if got := parseTime("2015-11-02 17:11"); got.IsZero() {
		t.Error("space-separated minute-precision should parse")
	}
	got := parseTime("2026-05-21T10:53:51")
	if got.IsZero() {
		t.Error("zoneless ISO (T-separator) should parse")
	}
	if got.Location().String() != "local" {
		t.Errorf("zoneless ISO should land in the unknownZone sentinel, got location %q", got.Location())
	}
}

func TestFirstNonEmpty(t *testing.T) {
	t.Parallel()
	if firstNonEmpty("", "y") != "y" {
		t.Error("non-empty wins")
	}
	if firstNonEmpty("", "") != "" {
		t.Error("all empty → empty")
	}
}

func TestCanonicalCarrierFromTrackingMoreCode(t *testing.T) {
	t.Parallel()
	// User passes a canonical id that maps to "dhl" → result is dhl_express.
	item := &trackingItem{CourierCode: "dhl"}
	if got := canonicalCarrier(trackage.CarrierDHLExpress, item); got != trackage.CarrierDHLExpress {
		t.Errorf("got %q", got)
	}
	// Unknown user input, but response courier_code matches a known canonical.
	if got := canonicalCarrier("", &trackingItem{CarrierCode: "dhl"}); got != trackage.CarrierDHLExpress {
		t.Errorf("from response carrier_code → %q", got)
	}
	// Nothing matches → returns user input verbatim.
	if got := canonicalCarrier("zzz", &trackingItem{}); got != "zzz" {
		t.Errorf("got %q", got)
	}
}

func TestCreateErrorNotAlreadyExists(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"meta":{"code":4110,"type":"BadRequest","message":"Invalid number"},"data":[]}`))
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	var ae *trackage.APIError
	if !errors.As(err, &ae) || ae.Code != "4110" {
		t.Errorf("expected APIError code=4110, got %+v", ae)
	}
}

func TestGetNoItems(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/trackings/create":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"meta":{"code":4101,"type":"BadRequest","message":"exists"},"data":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/trackings/get":
			_, _ = w.Write([]byte(`{"meta":{"code":200,"type":"Success","message":"ok"},"data":[]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if !errors.Is(err, trackage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetEnvelopeFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"meta":{"code":4101},"data":[]}`))
		case http.MethodGet:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"meta":{"code":4001,"type":"Unauthorized","message":"bad"},"data":[]}`))
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if !errors.Is(err, trackage.ErrAuth) {
		t.Errorf("expected ErrAuth, got %v", err)
	}
}

func TestGetMalformedData(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"meta":{"code":4101},"data":[]}`))
		case http.MethodGet:
			// `data` is an object instead of the expected array.
			_, _ = w.Write([]byte(`{"meta":{"code":200,"type":"OK","message":"x"},"data":{"items":"not-an-array"}}`))
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil || !strings.Contains(err.Error(), "parse list data") {
		t.Errorf("expected parse-list-data error, got %v", err)
	}
}

func TestDoSingleMalformedData(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// meta says success but data is the wrong shape (a string, not an object).
		_, _ = w.Write([]byte(`{"meta":{"code":200,"type":"OK","message":"x"},"data":"not-an-item"}`))
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil || !strings.Contains(err.Error(), "parse data") {
		t.Errorf("expected parse-data error, got %v", err)
	}
}

func TestMalformedEnvelope(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil || !strings.Contains(err.Error(), "parse envelope") {
		t.Errorf("expected envelope parse error, got %v", err)
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

// postOk4101GetFailTransport returns 4101 on POST (triggering the get
// fallback) and then a transport error on GET. Used to exercise the
// "fallback get itself fails" branch.
type postOk4101GetFailTransport struct{}

func (postOk4101GetFailTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPost {
		body := `{"meta":{"code":4101,"type":"BadRequest","message":"exists"},"data":[]}`
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{},
		}, nil
	}
	return nil, errors.New("get boom")
}

func TestGetFallbackTransportError(t *testing.T) {
	t.Parallel()
	tr := New(Config{
		APIKey:     "k",
		HTTPClient: &http.Client{Transport: postOk4101GetFailTransport{}},
	})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil || !strings.Contains(err.Error(), "get boom") {
		t.Fatalf("expected get-fallback transport error, got %v", err)
	}
}
