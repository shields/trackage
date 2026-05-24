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
