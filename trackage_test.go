package trackage

import (
	"errors"
	"testing"
)

func TestAllStatuses(t *testing.T) {
	t.Parallel()
	got := AllStatuses()
	want := []Status{
		StatusPending, StatusInTransit, StatusDelivered, StatusException, StatusUnknown,
	}
	if len(got) != len(want) {
		t.Fatalf("AllStatuses len = %d, want %d", len(got), len(want))
	}
	for i, s := range want {
		if got[i] != s {
			t.Errorf("AllStatuses[%d] = %q, want %q", i, got[i], s)
		}
	}
}

func TestAPIErrorError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  APIError
		want string
	}{
		{"code and message", APIError{Backend: "shippo", Code: "404", Message: "not found"}, "shippo: 404: not found"},
		{"message only", APIError{Backend: "shippo", Message: "bad request"}, "shippo: bad request"},
		{"code only", APIError{Backend: "shippo", Code: "EUNAUTH"}, "shippo: EUNAUTH"},
		{"nothing", APIError{Backend: "shippo", StatusCode: 500}, "shippo: http 500"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.err.Error(); got != c.want {
				t.Errorf("Error() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAPIErrorUnwrap(t *testing.T) {
	t.Parallel()
	err := &APIError{Backend: "shippo", Cause: ErrAuth}
	if !errors.Is(err, ErrAuth) {
		t.Error("errors.Is should succeed via Unwrap")
	}
	bare := &APIError{Backend: "shippo"}
	if errors.Is(bare, ErrAuth) {
		t.Error("errors.Is should not match a bare APIError")
	}
}

func TestLookupAndAllCarriers(t *testing.T) {
	t.Parallel()
	c, ok := LookupCarrier(CarrierUSPS)
	if !ok || c.ID != CarrierUSPS || c.Shippo != "usps" {
		t.Errorf("LookupCarrier(USPS) = %+v ok=%v", c, ok)
	}
	if _, ok := LookupCarrier("not-a-real-carrier"); ok {
		t.Error("LookupCarrier(nonsense) should return ok=false")
	}
	all := AllCarriers()
	if len(all) < 10 {
		t.Errorf("AllCarriers returned %d, expected at least 10", len(all))
	}
	// AllCarriers returns a copy: mutating the result should not affect the table.
	all[0].ID = "mutated"
	again := AllCarriers()
	if again[0].ID == "mutated" {
		t.Error("AllCarriers returned a live reference, not a copy")
	}
}
