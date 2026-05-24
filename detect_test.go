package trackage

import "testing"

func TestDetectCarrier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		number string
		want   string
	}{
		// UPS — 1Z prefix.
		{"1Z999AA10123456784", CarrierUPS},
		{"1z999aa10123456784", CarrierUPS}, // case insensitive

		// USPS IMpb — 22 digits beginning 91-95.
		{"9400111899223067387543", CarrierUSPS},
		{"9205590164917312751089", CarrierUSPS},

		// FedEx — 12 or 15 digits, no leading 9 ambiguity.
		{"123456789012", CarrierFedEx},
		{"123456789012345", CarrierFedEx},

		// DHL Express — 10 or 11 digits.
		{"1234567890", CarrierDHLExpress},
		{"12345678901", CarrierDHLExpress},

		// UPU S10 international.
		{"RR123456789CN", CarrierChinaPost},
		{"EA123456789US", CarrierUSPS},
		{"AB123456789GB", CarrierRoyalMail},
		{"CP123456789AU", CarrierAustraliaPost},
		{"CA123456789FR", CarrierLaPoste},
		{"RR123456789JP", CarrierJapanPost},
		{"LM123456789DE", CarrierDeutschePost},
		{"PP123456789BR", CarrierCorreiosBrazil},
		{"  RR123456789CN  ", CarrierChinaPost}, // whitespace
		{"RR-1234-56789-CN", CarrierChinaPost},  // hyphens stripped

		// S10 from a country we don't expose — falls through to "".
		{"RR123456789NZ", ""},

		// Garbage.
		{"", ""},
		{"hello world", ""},
		{"123", ""},
	}

	for _, c := range cases {
		got := DetectCarrier(c.number)
		if got != c.want {
			t.Errorf("DetectCarrier(%q) = %q, want %q", c.number, got, c.want)
		}
	}
}
