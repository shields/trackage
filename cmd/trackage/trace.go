package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
)

// traceWriter is the sink for the TRACKAGE_TRACE wire log. Stderr in
// production; tests override it to capture output.
var traceWriter io.Writer = os.Stderr

// traceEnabled reports whether TRACKAGE_TRACE asks for HTTP tracing.
// Only a small set of common truthy values turns tracing on; everything
// else (including "off", "no", "disabled") is treated as off so a user
// who sets TRACKAGE_TRACE=off to disable tracing is not surprised by
// secret-leaking trace output on stderr.
func traceEnabled() bool {
	switch strings.ToLower(os.Getenv("TRACKAGE_TRACE")) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	}
	return false
}

// newTraceClient returns an *http.Client whose transport logs requests
// and responses to traceWriter. Bodies are read into memory and
// replayed for the wrapped transport, so the upstream call is
// unaffected; large response bodies are echoed in full.
func newTraceClient() *http.Client {
	return &http.Client{Transport: newTraceTransport(http.DefaultTransport, traceWriter)}
}

func newTraceTransport(inner http.RoundTripper, w io.Writer) http.RoundTripper {
	return &traceTransport{inner: inner, w: w}
}

// traceTransport is the http.RoundTripper that wraps the real transport
// and writes a curl-style trace (> for request, < for response) to w.
type traceTransport struct {
	inner http.RoundTripper
	w     io.Writer
}

// sensitiveHeaders names headers whose values traceTransport must redact.
// Every backend's auth header lives here. Adding a backend that uses a
// new header name MUST update this map or the API key will leak into
// the trace output.
var sensitiveHeaders = map[string]struct{}{
	"authorization":       {}, // shippo (ShippoToken) and easypost (Basic)
	"cookie":              {},
	"proxy-authorization": {},
	"set-cookie":          {},
	"17token":             {}, // seventeentrack
	"tracking-api-key":    {}, // trackingmore
}

func (t *traceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request before we swap req.Body for our buffered
	// replacement: net/http documents that a RoundTripper may consume
	// and close the body but must not otherwise mutate the request.
	req2 := req.Clone(req.Context())
	t.logRequest(req2)
	resp, err := t.inner.RoundTrip(req2)
	if err != nil {
		t.emitf("< error: %v\n\n", err)
		return nil, err
	}
	t.logResponse(resp)
	return resp, nil
}

// logRequest writes the request line, headers, and body to the trace
// sink. Body-read failures are reported into the trace stream AND
// re-attached to the replacement body, so the upstream transport (and
// ultimately the caller) sees the same error a non-traced request would
// have seen — we never want enabling tracing to silently send a
// truncated request body.
func (t *traceTransport) logRequest(req *http.Request) {
	t.emitf("> %s %s %s\n", req.Method, req.URL, req.Proto)
	t.writeHeaders(">", req.Header)
	t.emitf(">\n")
	if req.Body == nil {
		t.emitf("\n")
		return
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close() //nolint:errcheck // close errors on a fully-read body are inactionable
	if err != nil {
		t.emitf("> error reading body for trace: %v\n", err)
	}
	if len(body) > 0 {
		t.emitf("> %s\n", body)
	}
	t.emitf("\n")
	req.Body = newTraceBody(body, err)
}

// logResponse writes the response status, headers, and body to the
// trace sink. As with logRequest, body-read failures are reported into
// the trace stream and re-surfaced when the caller reads the
// replacement body, rather than handing them silently-truncated data.
func (t *traceTransport) logResponse(resp *http.Response) {
	if resp == nil {
		return
	}
	t.emitf("< %s %s\n", resp.Proto, resp.Status)
	t.writeHeaders("<", resp.Header)
	t.emitf("<\n")
	if resp.Body == nil {
		t.emitf("\n")
		return
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close() //nolint:errcheck // close errors on a fully-read body are inactionable
	if err != nil {
		t.emitf("< error reading body for trace: %v\n", err)
	}
	if len(body) > 0 {
		t.emitf("< %s\n", body)
	}
	t.emitf("\n")
	resp.Body = newTraceBody(body, err)
}

// traceBody replays a buffered body and, on exhaustion, surfaces any
// error that occurred while reading the original. If readErr is nil it
// behaves identically to io.NopCloser(bytes.NewReader(body)).
type traceBody struct {
	r       *bytes.Reader
	readErr error
}

func newTraceBody(body []byte, readErr error) io.ReadCloser {
	return &traceBody{r: bytes.NewReader(body), readErr: readErr}
}

func (b *traceBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err == io.EOF && b.readErr != nil {
		return n, b.readErr
	}
	return n, err
}

func (*traceBody) Close() error { return nil }

func (t *traceTransport) writeHeaders(prefix string, h http.Header) {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		value := strings.Join(h.Values(k), ", ")
		if _, redacted := sensitiveHeaders[strings.ToLower(k)]; redacted {
			value = "<redacted>"
		}
		t.emitf("%s %s: %s\n", prefix, k, value)
	}
}

// emitf writes a single trace line. Trace-stream write errors are
// inactionable for an instrumentation feature; we swallow them rather
// than propagating noise into the upstream RoundTrip.
func (t *traceTransport) emitf(format string, args ...any) {
	_, _ = fmt.Fprintf(t.w, format, args...) //nolint:errcheck // trace sink errors are inactionable
}
