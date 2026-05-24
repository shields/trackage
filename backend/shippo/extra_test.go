// Copyright © 2026 Michael Shields
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package shippo

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
	if got := (&Tracker{}).Name(); got != "shippo" {
		t.Errorf("Name = %q", got)
	}
}

func TestParseTimeAccepts(t *testing.T) {
	t.Parallel()
	// RFC3339 with ms (object_created style).
	if got := parseTime("2023-07-23T20:35:26.129Z"); got.IsZero() {
		t.Error("ms-precision RFC3339 should parse")
	}
	// RFC3339 without ms (status_date style).
	if got := parseTime("2023-07-23T13:03:00Z"); got.IsZero() {
		t.Error("second-precision RFC3339 should parse")
	}
	// Empty string returns zero.
	if got := parseTime(""); !got.IsZero() {
		t.Error("empty should return zero time")
	}
	// Garbage returns zero.
	if got := parseTime("not a date"); !got.IsZero() {
		t.Error("garbage should return zero time")
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
		{http.StatusInternalServerError, nil}, // 500 → no sentinel
	}
	for _, c := range cases {
		err := parseError(c.code, []byte(`{"detail":"x"}`))
		if c.want == nil {
			if errors.Is(err, trackage.ErrAuth) ||
				errors.Is(err, trackage.ErrNotFound) ||
				errors.Is(err, trackage.ErrRateLimited) {
				t.Errorf("code %d should not map to a sentinel, got %v", c.code, err)
			}
			continue
		}
		if !errors.Is(err, c.want) {
			t.Errorf("code %d → %v, want %v", c.code, err, c.want)
		}
	}
}

func TestExtractMessageVariants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"detail object", `{"detail":"oops"}`, "oops"},
		{"field array", `{"carrier":["Not supported."]}`, "carrier: Not supported."},
		{"field string", `{"carrier":"Not supported."}`, "carrier: Not supported."},
		{"field other types are skipped", `{"x":42}`, `{"x":42}`}, // falls through to raw body
		{"empty body", ``, ""},
		{"non-JSON body", `pure junk`, "pure junk"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := extractMessage([]byte(c.body)); got != c.want {
				t.Errorf("extractMessage(%q) = %q, want %q", c.body, got, c.want)
			}
		})
	}
}

func TestNetworkErrorBubbles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	srv.Close() // immediately close so subsequent dials refuse
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "9400111899223067387543")
	if err == nil {
		t.Fatal("expected network error")
	}
}

func TestResolveCarrierUnknownPassthrough(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Unknown canonical → passes through verbatim.
		if !strings.Contains(r.URL.Path, "weird_carrier") {
			t.Errorf("expected weird_carrier in path, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"carrier":"weird_carrier","tracking_number":"x","tracking_status":{"status":"UNKNOWN"}}`))
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	if _, err := tr.Track(context.Background(), "weird_carrier", "x"); err != nil {
		t.Fatalf("Track: %v", err)
	}
}

func TestResolveCarrierUnsupportedCanonical(t *testing.T) {
	t.Parallel()
	// china_post has empty Shippo code in the table → ErrUnsupportedCarrier.
	tr := New(Config{APIKey: "k"})
	_, err := tr.Track(context.Background(), trackage.CarrierChinaPost, "RR123456789CN")
	if !errors.Is(err, trackage.ErrUnsupportedCarrier) {
		t.Errorf("expected ErrUnsupportedCarrier, got %v", err)
	}
}

func TestLocationString(t *testing.T) {
	t.Parallel()
	var l *location
	if got := l.String(); got != "" {
		t.Errorf("nil location → %q, want empty", got)
	}
	full := &location{City: "Houston", State: "TX", Zip: "77001", Country: "US"}
	if got := full.String(); got != "Houston, TX, 77001, US" {
		t.Errorf("full location → %q", got)
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

// errReadCloser is an io.ReadCloser that always errors on Read; used
// to exercise the io.ReadAll failure branch.
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
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "9400111899223067387543")
	if err == nil || !strings.Contains(err.Error(), "read boom") {
		t.Fatalf("expected read body error, got %v", err)
	}
}

func TestNewRequestError(t *testing.T) {
	t.Parallel()
	// A control character in the base URL makes http.NewRequestWithContext
	// fail at url.Parse.
	tr := New(Config{APIKey: "k", BaseURL: "http://example.com\x7f"})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil {
		t.Fatal("expected NewRequest error")
	}
}

// TestURLEscapesTrackingNumber confirms that special characters in the
// tracking number do not leak past Shippo's URL — they must be
// percent-encoded so they cannot split the path or inject a query
// string against api.goshippo.com.
func TestURLEscapesTrackingNumber(t *testing.T) {
	t.Parallel()
	var seenRawPath, seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RawPath preserves the encoding the client actually sent; Path
		// would decode and lose the distinction between a literal slash
		// and a percent-encoded one. EscapedPath() falls back to Path
		// when RawPath is empty (everything was already canonical).
		seenRawPath = r.URL.EscapedPath()
		seenQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "abc?token=leak#frag/extra")
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	if seenQuery != "" {
		t.Errorf("URL grew an unexpected query string %q — escaping is broken", seenQuery)
	}
	// The number must arrive as a single path segment whose raw form
	// percent-encodes every '?', '#', and '/'. The expected escaping is
	// the exact form url.PathEscape produces.
	wantSeg := "abc%3Ftoken=leak%23frag%2Fextra"
	if seenRawPath != "/tracks/usps/"+wantSeg {
		t.Errorf("raw path = %q, want %q", seenRawPath, "/tracks/usps/"+wantSeg)
	}
}
