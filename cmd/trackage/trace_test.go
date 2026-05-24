package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"msrl.dev/trackage"
	"msrl.dev/trackage/backend/easypost"
)

// roundTripperFunc adapts a function to http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// errBody is an io.ReadCloser that fails on first Read; tests use it
// to exercise the trace transport's body-read error paths.
type errBody struct{ err error }

func (e *errBody) Read([]byte) (int, error) { return 0, e.err }
func (*errBody) Close() error               { return nil }

// closeIfPossible closes resp.Body if non-nil; tests use it to satisfy
// the bodyclose linter without sprinkling nil checks at every call.
func closeIfPossible(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}

//nolint:paralleltest // mutates env
func TestTraceEnabled(t *testing.T) {
	cases := map[string]bool{
		// Falsy variants — common ways to spell "off" should all leave
		// tracing disabled. Otherwise `TRACKAGE_TRACE=off` would
		// counter-intuitively enable secret-leaking trace output.
		"":         false,
		"0":        false,
		"false":    false,
		"False":    false,
		"no":       false,
		"off":      false,
		"disabled": false,
		"n":        false,
		// Truthy variants.
		"1":    true,
		"true": true,
		"on":   true,
		"yes":  true,
		"YES":  true,
	}
	for v, want := range cases {
		t.Setenv("TRACKAGE_TRACE", v)
		if got := traceEnabled(); got != want {
			t.Errorf("traceEnabled with TRACKAGE_TRACE=%q = %v, want %v", v, got, want)
		}
	}
}

func TestTraceTransportSuccess(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	inner := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		// The inner transport should still see the request body bytes
		// even though the trace layer drained the reader.
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"x":1}` {
			t.Errorf("inner saw body %q, want %q", body, `{"x":1}`)
		}
		return &http.Response{
			Proto:      "HTTP/1.1",
			Status:     "201 Created",
			StatusCode: http.StatusCreated,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"Set-Cookie":   []string{"sid=abc"},
			},
			Body: io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	})
	tt := newTraceTransport(inner, &buf)
	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost,
		"http://example.com/x", strings.NewReader(`{"x":1}`),
	)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Basic shhhh")
	req.Header.Set("Content-Type", "application/json")
	resp, err := tt.RoundTrip(req)
	defer closeIfPossible(resp)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"ok":true}` {
		t.Errorf("caller saw response body %q, want %q", body, `{"ok":true}`)
	}
	out := buf.String()
	for _, want := range []string{
		"> POST http://example.com/x HTTP/1.1",
		"> Authorization: <redacted>",
		"> Content-Type: application/json",
		`> {"x":1}`,
		"< HTTP/1.1 201 Created",
		"< Set-Cookie: <redacted>",
		`< {"ok":true}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("trace missing %q\nfull trace:\n%s", want, out)
		}
	}
	if strings.Contains(out, "shhhh") || strings.Contains(out, "sid=abc") {
		t.Errorf("trace leaked a redacted value:\n%s", out)
	}
}

func TestTraceTransportNoBodies(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	inner := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			Proto: "HTTP/1.1", Status: "204 No Content", StatusCode: http.StatusNoContent,
		}, nil
	})
	tt := newTraceTransport(inner, &buf)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/", http.NoBody)
	req.Body = nil // GET with explicit nil body
	resp, err := tt.RoundTrip(req)
	defer closeIfPossible(resp)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "> GET http://example.com/") {
		t.Errorf("missing request line:\n%s", out)
	}
	if !strings.Contains(out, "< HTTP/1.1 204 No Content") {
		t.Errorf("missing response line:\n%s", out)
	}
}

func TestTraceTransportInnerError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	wantErr := errors.New("dial failed")
	inner := roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, wantErr })
	tt := newTraceTransport(inner, &buf)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/", http.NoBody)
	resp, err := tt.RoundTrip(req)
	defer closeIfPossible(resp)
	if !errors.Is(err, wantErr) {
		t.Errorf("got err=%v, want wantErr to propagate", err)
	}
	if !strings.Contains(buf.String(), "< error: dial failed") {
		t.Errorf("missing error log:\n%s", buf.String())
	}
}

func TestTraceTransportRequestBodyReadError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	innerCalled := false
	inner := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		innerCalled = true
		return &http.Response{
			Proto: "HTTP/1.1", Status: "200 OK", StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader("")),
		}, nil
	})
	tt := newTraceTransport(inner, &buf)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.com/", http.NoBody)
	req.Body = &errBody{err: errors.New("read busted")}
	resp, err := tt.RoundTrip(req)
	defer closeIfPossible(resp)
	if err != nil {
		t.Fatalf("RoundTrip: %v, want nil (tracing is observational)", err)
	}
	if !innerCalled {
		t.Error("inner should still be called when request body read fails (observational tracing)")
	}
	if !strings.Contains(buf.String(), "error reading body for trace: read busted") {
		t.Errorf("trace did not record body-read error:\n%s", buf.String())
	}
}

func TestTraceTransportResponseBodyReadError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	inner := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			Proto: "HTTP/1.1", Status: "200 OK", StatusCode: http.StatusOK,
			Body: &errBody{err: errors.New("read busted")},
		}, nil
	})
	tt := newTraceTransport(inner, &buf)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/", http.NoBody)
	resp, err := tt.RoundTrip(req)
	defer closeIfPossible(resp)
	if err != nil {
		t.Fatalf("RoundTrip: %v, want nil (tracing is observational)", err)
	}
	if resp == nil {
		t.Fatal("RoundTrip returned nil response; observational tracing must not drop the real response")
	}
	if !strings.Contains(buf.String(), "error reading body for trace: read busted") {
		t.Errorf("trace did not record body-read error:\n%s", buf.String())
	}
}

// TestSensitiveHeadersRedacted confirms that the auth header for every
// supported backend is in the redaction allowlist. Adding a backend
// that uses a new header MUST update sensitiveHeaders or its API key
// will leak into trace output.
func TestSensitiveHeadersRedacted(t *testing.T) {
	t.Parallel()
	for _, h := range []string{
		"Authorization",    // shippo, easypost
		"17token",          // seventeentrack
		"Tracking-Api-Key", // trackingmore
		"Cookie",
		"Set-Cookie",
		"Proxy-Authorization",
	} {
		if _, ok := sensitiveHeaders[strings.ToLower(h)]; !ok {
			t.Errorf("sensitiveHeaders missing %q — credential will leak in trace output", h)
		}
	}
}

// TestTraceRedactsBackendAuthHeaders runs every redaction case
// end-to-end through traceTransport.RoundTrip to lock in the behavior
// at the trace-sink boundary.
func TestTraceRedactsBackendAuthHeaders(t *testing.T) {
	t.Parallel()
	cases := []struct {
		header, value string
	}{
		{"Authorization", "ShippoToken shippo-secret"},
		{"17token", "seventeentrack-secret"},
		{"Tracking-Api-Key", "trackingmore-secret"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		inner := roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				Proto: "HTTP/1.1", Status: "200 OK", StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader("{}")),
			}, nil
		})
		tt := newTraceTransport(inner, &buf)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/", http.NoBody)
		req.Header.Set(c.header, c.value)
		resp, err := tt.RoundTrip(req)
		closeIfPossible(resp)
		if err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		out := buf.String()
		if strings.Contains(out, c.value) {
			t.Errorf("%s value leaked into trace output:\n%s", c.header, out)
		}
		if !strings.Contains(out, "<redacted>") {
			t.Errorf("%s was not redacted:\n%s", c.header, out)
		}
	}
}

func TestNewTraceClientWrapsDefaultTransport(t *testing.T) {
	t.Parallel()
	got := newTraceClient()
	if _, ok := got.Transport.(*traceTransport); !ok {
		t.Errorf("Transport = %T, want *traceTransport", got.Transport)
	}
}

func TestResolveBackendInstallsTraceClient(t *testing.T) {
	// Override traceWriter so we capture the request log.
	var captured bytes.Buffer
	oldWriter := traceWriter
	t.Cleanup(func() { traceWriter = oldWriter })
	traceWriter = &captured

	// Stand up a fake backend that records the actual HTTP request, so
	// we can confirm the trace transport was inserted end-to-end.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"unknown","tracking_details":[]}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("TRACKAGE_TRACE", "1")
	t.Setenv("EASYPOST_API_KEY", "k")
	t.Setenv("TRACKAGE_BACKEND", "easypost")

	// Swap in a registry whose easypost builder points at our test
	// server, but otherwise behaves like the real one.
	old := backendRegistry
	t.Cleanup(func() { backendRegistry = old })
	backendRegistry = []backendInfo{{
		Name: "easypost", DisplayName: "EasyPost", EnvKey: "EASYPOST_API_KEY",
		Build: func(k string, c *http.Client) trackage.Tracker {
			return easypost.New(easypost.Config{APIKey: k, BaseURL: srv.URL, HTTPClient: c})
		},
	}}

	tr, _, err := resolveBackend(context.Background(), "", "", config{})
	if err != nil {
		t.Fatalf("resolveBackend: %v", err)
	}
	if _, err := tr.Track(context.Background(), "", "EZ1000000001"); err != nil {
		t.Fatalf("Track: %v", err)
	}
	if got := captured.String(); !strings.Contains(got, "> POST "+srv.URL) {
		t.Errorf("trace did not capture the upstream request:\n%s", got)
	}
}
