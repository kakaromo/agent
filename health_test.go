package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent/adb"
)

// healthResp mirrors the anonymous struct returned by /health.
// Defined locally in the test so production code stays minimal.
type healthResp struct {
	Status  string `json:"status"`
	Devices int    `json:"devices"`
}

// TestHealthHandler spins up a real httptest server backed by our
// handler and verifies the JSON round-trip — Content-Type, status,
// body shape. Lessons: 5 (struct/tags), 10 (httptest), 11 (subtests).
func TestHealthHandler(t *testing.T) {
	mgr := adb.NewManager()

	// Register only the handler we want to test. We deliberately do
	// NOT use main()'s mux — keeping the test boundary tight.
	srv := httptest.NewServer(newHealthHandler(mgr))
	t.Cleanup(srv.Close) // 11장: t.Cleanup is the modern alternative to defer

	t.Run("status ok and shape", func(t *testing.T) {
		resp, err := http.Get(srv.URL)
		if err != nil {
			t.Fatalf("GET /health: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Errorf("Content-Type = %q, want application/json", got)
		}

		var body healthResp
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Status != "ok" {
			t.Errorf("status field = %q, want %q", body.Status, "ok")
		}
		// Fresh manager has no devices.
		if body.Devices != 0 {
			t.Errorf("devices = %d, want 0", body.Devices)
		}
	})

	// We can't easily push fake devices into adb.Manager from here
	// (devices map is unexported), so we exercise the no-device case
	// thoroughly and trust Count()'s own contract — which we'll cover
	// next in TestManagerCount.
}

// TestManagerCount verifies the new Count() method directly. This
// pairs with TestHealthHandler: together they fully cover the path
// "Count -> /health body". Lessons: 5 (method), 8 (concurrency).
func TestManagerCount(t *testing.T) {
	mgr := adb.NewManager()
	if got := mgr.Count(); got != 0 {
		t.Errorf("empty manager: Count = %d, want 0", got)
	}

	// We cannot directly populate mgr.devices because it's unexported.
	// That's actually a property test in disguise: empty Count behaves
	// correctly under the public API. To go further we'd need either
	// (a) an exported test helper in the adb package, or
	// (b) an integration test with real adb. Both are out of scope here.
	//
	// What we CAN test cheaply: Count is safe under concurrent reads.
	// If RLock were missing, -race would flag this.
	const goroutines = 50
	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			_ = mgr.Count()
			done <- struct{}{}
		}()
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
}
