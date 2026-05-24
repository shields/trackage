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

// Package trackage is a unified Go library for tracking parcels across
// multiple shipping providers.
//
// trackage normalizes four backends (Shippo, EasyPost, 17Track, TrackingMore)
// behind a single [Tracker] interface so application code does not have to
// care which provider returned a given update.
//
// # Quick start
//
//	import (
//	    "context"
//	    "fmt"
//
//	    "msrl.dev/trackage"
//	    "msrl.dev/trackage/backend/shippo"
//	)
//
//	func main() {
//	    t := shippo.New(shippo.Config{APIKey: "shippo_test_..."})
//	    r, err := t.Track(context.Background(), "usps", "9400111899223067387543")
//	    if err != nil {
//	        panic(err)
//	    }
//	    fmt.Println(r.Status, r.Description)
//	}
//
// The companion command-line tool lives under cmd/trackage.
package trackage
