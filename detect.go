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

package trackage

import (
	"regexp"
	"strings"
	"unicode"
)

// DetectCarrier guesses the canonical trackage carrier id from a tracking
// number's format. Returns "" when nothing matches with reasonable
// confidence — callers should fall back to their backend's auto-detect
// or surface the ambiguity to the user.
//
// Detection covers:
//   - UPS:          starts "1Z" + 16 alphanumerics
//   - USPS IMpb:    22 digits beginning with 91-95
//   - FedEx:        12 or 15 digits (no overlap with USPS IMpb)
//   - DHL Express:  10 or 11 digits
//   - International UPU S10 (ISO 15459 / S10 standard, 13 chars,
//     2 service letters + 9 digits + 2-letter origin country code):
//     US→USPS, GB→Royal Mail, CA→Canada Post, AU→Australia Post,
//     FR→La Poste, DE→Deutsche Post, CN→China Post, JP→Japan Post,
//     IT→Poste Italiane, BR→Correios Brazil.
//
// Numbers are normalized by stripping whitespace and uppercasing before
// matching. This is best-effort: there are genuine ambiguities (a 15-digit
// FedEx and a 15-digit USPS Certified Mail look identical), and trackage
// errs on the side of returning "" when it can't decide.
func DetectCarrier(number string) string {
	n := strings.ToUpper(strings.Map(func(r rune) rune {
		// Strip any whitespace (tabs, newlines, NBSP, etc.) plus the
		// common decorative separators carriers and labels print
		// between number groups.
		if unicode.IsSpace(r) {
			return -1
		}
		switch r {
		case '-', '.', '_', '(', ')':
			return -1
		}
		return r
	}, number))
	if n == "" {
		return ""
	}

	// UPS 1Z prefix is unambiguous.
	if reUPS.MatchString(n) {
		return CarrierUPS
	}

	// UPU S10 international: pull the origin country and map it.
	if m := reS10.FindStringSubmatch(n); m != nil {
		if id, ok := s10Country[m[1]]; ok {
			return id
		}
	}

	// USPS IMpb (22 digits, leading 91-95). Some FedEx SmartPost numbers
	// share the leading 92 prefix, but USPS is by far the more common
	// owner of that namespace, so we attribute it to USPS.
	if reUSPSIMpb.MatchString(n) {
		return CarrierUSPS
	}

	// FedEx 12 or 15 digits. (20- and 22-digit FedEx forms are
	// SmartPost / USPS handoffs and we leave them to USPS detection.)
	if reFedEx.MatchString(n) {
		return CarrierFedEx
	}

	// DHL Express 10 or 11 digits. This range overlaps with several
	// regional services; we keep it last so the more-specific matchers
	// above win when they can.
	if reDHLExpress.MatchString(n) {
		return CarrierDHLExpress
	}

	return ""
}

var (
	reUPS        = regexp.MustCompile(`^1Z[0-9A-Z]{16}$`)
	reUSPSIMpb   = regexp.MustCompile(`^9[1-5][0-9]{20}$`)
	reFedEx      = regexp.MustCompile(`^[0-9]{12}$|^[0-9]{15}$`)
	reDHLExpress = regexp.MustCompile(`^[0-9]{10,11}$`)
	reS10        = regexp.MustCompile(`^[A-Z]{2}[0-9]{9}([A-Z]{2})$`)
)

// s10Country maps a UPU S10 origin-country code to a canonical carrier.
// Only countries whose national post we expose as a canonical id are
// listed; other countries fall through and return "" from DetectCarrier.
var s10Country = map[string]string{
	"US": CarrierUSPS,
	"GB": CarrierRoyalMail,
	"CA": CarrierCanadaPost,
	"AU": CarrierAustraliaPost,
	"FR": CarrierLaPoste,
	"DE": CarrierDeutschePost,
	"CN": CarrierChinaPost,
	"JP": CarrierJapanPost,
	"IT": CarrierPosteItaliane,
	"BR": CarrierCorreiosBrazil,
}
