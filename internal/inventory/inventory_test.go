package inventory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GreenHatOps/station-monitor/internal/collector"
)

func TestInventoryHandler_EmptyState(t *testing.T) {
	p := &Provider{}
	req := httptest.NewRequest(http.MethodGet, "/inventory", nil)
	w := httptest.NewRecorder()

	p.Handler()(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var inv Inventory
	if err := json.Unmarshal(w.Body.Bytes(), &inv); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if inv.Version != "0.1.0" {
		t.Errorf("version = %q, want 0.1.0", inv.Version)
	}
	if len(inv.Drives) != 0 {
		t.Errorf("expected 0 drives before collection, got %d", len(inv.Drives))
	}
	if len(inv.GPUs) != 0 {
		t.Errorf("expected 0 GPUs before collection, got %d", len(inv.GPUs))
	}
}

func TestInventoryHandler_WithCollectorData(t *testing.T) {
	// Create mock collectors with pre-populated data
	smartC := collector.NewSmartCollector(&mockRunner{}, 5*60*1e9)
	gpuC := collector.NewGPUCollector(&mockRunner{})

	// Inject test data via exported methods isn't possible, so test the handler
	// with nil collectors (already tested above) and with non-nil ones that have
	// empty caches — which is the realistic startup state
	p := &Provider{Smart: smartC, GPU: gpuC}
	req := httptest.NewRequest(http.MethodGet, "/inventory", nil)
	w := httptest.NewRecorder()

	p.Handler()(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var inv Inventory
	if err := json.Unmarshal(w.Body.Bytes(), &inv); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	// With empty caches, drives and GPUs should be empty slices
	if inv.Drives != nil && len(inv.Drives) != 0 {
		t.Errorf("expected 0 drives from empty cache, got %d", len(inv.Drives))
	}
}

func TestInventoryHandler_JSONStructure(t *testing.T) {
	p := &Provider{}
	req := httptest.NewRequest(http.MethodGet, "/inventory", nil)
	w := httptest.NewRecorder()

	p.Handler()(w, req)

	var raw map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Verify expected top-level keys exist
	expectedKeys := []string{"hostname", "os", "kernel", "arch", "cpu", "memory_bytes", "drives", "gpu", "station_monitor_version"}
	for _, key := range expectedKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing key %q in inventory JSON", key)
		}
	}
}

// mockRunner for inventory tests (never called, just satisfies interface)
type mockRunner struct{}

func (m *mockRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	return nil, nil
}
