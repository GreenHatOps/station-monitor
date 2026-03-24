package collector

import (
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/client_golang/prometheus"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("failed to load fixture %s: %v", name, err)
	}
	return data
}

func collectMetrics(t *testing.T, c prometheus.Collector) []*dto.MetricFamily {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}
	return families
}

func findMetric(families []*dto.MetricFamily, name string) *dto.MetricFamily {
	for _, f := range families {
		if f.GetName() == name {
			return f
		}
	}
	return nil
}

func findMetricValue(families []*dto.MetricFamily, name string) (float64, bool) {
	f := findMetric(families, name)
	if f == nil || len(f.GetMetric()) == 0 {
		return 0, false
	}
	m := f.GetMetric()[0]
	if m.Gauge != nil {
		return m.Gauge.GetValue(), true
	}
	if m.Counter != nil {
		return m.Counter.GetValue(), true
	}
	return 0, false
}

func getLabelValue(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

// --- Discovery Tests ---

func TestSmartDiscovery_NVMe(t *testing.T) {
	scanData := loadFixture(t, "smartctl-scan-nvme.json")
	nvmeData := loadFixture(t, "smartctl-nvme-attrs.json")

	runner := &MockRunner{Responses: map[string]MockResponse{
		"smartctl --scan --json=c":           {Output: scanData},
		"smartctl --json=c -a /dev/nvme0":    {Output: nvmeData},
	}}

	sc := NewSmartCollector(runner, 5*time.Minute)
	sc.scanAndCollect()

	drives := sc.Drives()
	if len(drives) != 1 {
		t.Fatalf("expected 1 drive, got %d", len(drives))
	}
	if drives[0].Type != "nvme" {
		t.Errorf("expected type nvme, got %s", drives[0].Type)
	}
	if drives[0].Name != "nvme0" {
		t.Errorf("expected name nvme0, got %s", drives[0].Name)
	}
	if drives[0].Model != "Samsung SSD 990 EVO Plus 2TB" {
		t.Errorf("expected model Samsung SSD 990 EVO Plus 2TB, got %s", drives[0].Model)
	}
}

func TestSmartDiscovery_Mixed(t *testing.T) {
	scanData := loadFixture(t, "smartctl-scan-mixed.json")
	nvmeData := loadFixture(t, "smartctl-nvme-attrs.json")
	sataData := loadFixture(t, "smartctl-sata-attrs.json")

	runner := &MockRunner{Responses: map[string]MockResponse{
		"smartctl --scan --json=c":        {Output: scanData},
		"smartctl --json=c -a /dev/nvme0": {Output: nvmeData},
		"smartctl --json=c -a /dev/sda":   {Output: sataData},
	}}

	sc := NewSmartCollector(runner, 5*time.Minute)
	sc.scanAndCollect()

	drives := sc.Drives()
	if len(drives) != 2 {
		t.Fatalf("expected 2 drives, got %d", len(drives))
	}
	if drives[0].Type != "nvme" {
		t.Errorf("drive 0: expected nvme, got %s", drives[0].Type)
	}
	if drives[1].Type != "sat" {
		t.Errorf("drive 1: expected sat, got %s", drives[1].Type)
	}
}

func TestSmartDiscovery_Empty(t *testing.T) {
	scanData := loadFixture(t, "smartctl-scan-empty.json")

	runner := &MockRunner{Responses: map[string]MockResponse{
		"smartctl --scan --json=c": {Output: scanData},
	}}

	sc := NewSmartCollector(runner, 5*time.Minute)
	sc.scanAndCollect()

	if len(sc.Drives()) != 0 {
		t.Errorf("expected 0 drives, got %d", len(sc.Drives()))
	}
}

func TestSmartDiscovery_ScanFails(t *testing.T) {
	runner := &MockRunner{Responses: map[string]MockResponse{
		"smartctl --scan --json=c": {Err: fmt.Errorf("not found")},
	}}

	sc := NewSmartCollector(runner, 5*time.Minute)
	sc.scanAndCollect()

	if len(sc.Drives()) != 0 {
		t.Errorf("expected 0 drives on scan failure, got %d", len(sc.Drives()))
	}
}

func TestSmartDiscovery_ScanPermissionDenied(t *testing.T) {
	runner := &MockRunner{Responses: map[string]MockResponse{
		"smartctl --scan --json=c": {Err: fmt.Errorf("exit status 1: permission denied")},
	}}

	sc := NewSmartCollector(runner, 5*time.Minute)
	sc.scanAndCollect()

	if len(sc.Drives()) != 0 {
		t.Errorf("expected 0 drives on permission denied, got %d", len(sc.Drives()))
	}
}

func TestSmartDiscovery_InvalidJSON(t *testing.T) {
	runner := &MockRunner{Responses: map[string]MockResponse{
		"smartctl --scan --json=c": {Output: []byte("not json at all")},
	}}

	sc := NewSmartCollector(runner, 5*time.Minute)
	sc.scanAndCollect()

	// gjson returns empty on invalid JSON, so 0 drives
	if len(sc.Drives()) != 0 {
		t.Errorf("expected 0 drives on invalid JSON, got %d", len(sc.Drives()))
	}
}

// --- NVMe Metrics Tests ---

func TestSmartNVMe_Metrics(t *testing.T) {
	scanData := loadFixture(t, "smartctl-scan-nvme.json")
	nvmeData := loadFixture(t, "smartctl-nvme-attrs.json")

	runner := &MockRunner{Responses: map[string]MockResponse{
		"smartctl --scan --json=c":        {Output: scanData},
		"smartctl --json=c -a /dev/nvme0": {Output: nvmeData},
	}}

	sc := NewSmartCollector(runner, 5*time.Minute)
	sc.scanAndCollect()

	families := collectMetrics(t, sc)

	tests := []struct {
		metric string
		want   float64
	}{
		{"station_smart_temperature_celsius", 29},
		{"station_smart_available_spare_ratio", 1.0},             // 100 / 100
		{"station_smart_available_spare_threshold_ratio", 0.1},   // 10 / 100
		{"station_smart_percentage_used_ratio", 0.0},             // 0 / 100
		{"station_smart_power_on_hours_total", 42},
		{"station_smart_power_cycles_total", 12},
		{"station_smart_unsafe_shutdowns_total", 3},
		{"station_smart_media_errors_total", 0},
		{"station_smart_critical_warning", 0},
		{"station_smart_data_read_bytes_total", 30936 * 512000},
		{"station_smart_data_written_bytes_total", 84508 * 512000},
	}

	for _, tt := range tests {
		val, ok := findMetricValue(families, tt.metric)
		if !ok {
			t.Errorf("metric %s not found", tt.metric)
			continue
		}
		if math.Abs(val-tt.want) > 0.001 {
			t.Errorf("%s = %v, want %v", tt.metric, val, tt.want)
		}
	}
}

func TestSmartNVMe_WornDrive(t *testing.T) {
	scanData := loadFixture(t, "smartctl-scan-nvme.json")
	wornData := loadFixture(t, "smartctl-nvme-worn.json")

	runner := &MockRunner{Responses: map[string]MockResponse{
		"smartctl --scan --json=c":        {Output: scanData},
		"smartctl --json=c -a /dev/nvme0": {Output: wornData},
	}}

	sc := NewSmartCollector(runner, 5*time.Minute)
	sc.scanAndCollect()
	families := collectMetrics(t, sc)

	// Verify worn drive values
	spare, ok := findMetricValue(families, "station_smart_available_spare_ratio")
	if !ok || math.Abs(spare-0.05) > 0.001 {
		t.Errorf("available_spare = %v, want 0.05", spare)
	}
	used, ok := findMetricValue(families, "station_smart_percentage_used_ratio")
	if !ok || math.Abs(used-0.95) > 0.001 {
		t.Errorf("percentage_used = %v, want 0.95", used)
	}
	errors, ok := findMetricValue(families, "station_smart_media_errors_total")
	if !ok || errors != 3 {
		t.Errorf("media_errors = %v, want 3", errors)
	}
}

func TestSmartNVMe_Labels(t *testing.T) {
	scanData := loadFixture(t, "smartctl-scan-nvme.json")
	nvmeData := loadFixture(t, "smartctl-nvme-attrs.json")

	runner := &MockRunner{Responses: map[string]MockResponse{
		"smartctl --scan --json=c":        {Output: scanData},
		"smartctl --json=c -a /dev/nvme0": {Output: nvmeData},
	}}

	sc := NewSmartCollector(runner, 5*time.Minute)
	sc.scanAndCollect()
	families := collectMetrics(t, sc)

	f := findMetric(families, "station_smart_temperature_celsius")
	if f == nil || len(f.GetMetric()) == 0 {
		t.Fatal("no temperature metric found")
	}
	m := f.GetMetric()[0]
	if got := getLabelValue(m, "device"); got != "nvme0" {
		t.Errorf("device label = %q, want nvme0", got)
	}
	if got := getLabelValue(m, "model"); got != "Samsung SSD 990 EVO Plus 2TB" {
		t.Errorf("model label = %q, want Samsung SSD 990 EVO Plus 2TB", got)
	}
	if got := getLabelValue(m, "type"); got != "nvme" {
		t.Errorf("type label = %q, want nvme", got)
	}
}

// --- SATA Metrics Tests ---

func TestSmartSATA_Metrics(t *testing.T) {
	scanData := loadFixture(t, "smartctl-scan-mixed.json")
	nvmeData := loadFixture(t, "smartctl-nvme-attrs.json")
	sataData := loadFixture(t, "smartctl-sata-attrs.json")

	runner := &MockRunner{Responses: map[string]MockResponse{
		"smartctl --scan --json=c":        {Output: scanData},
		"smartctl --json=c -a /dev/nvme0": {Output: nvmeData},
		"smartctl --json=c -a /dev/sda":   {Output: sataData},
	}}

	sc := NewSmartCollector(runner, 5*time.Minute)
	sc.scanAndCollect()
	families := collectMetrics(t, sc)

	// Find SATA-specific metrics
	f := findMetric(families, "station_smart_reallocated_sectors_total")
	if f == nil || len(f.GetMetric()) == 0 {
		t.Fatal("no reallocated_sectors metric found")
	}
	m := f.GetMetric()[0]
	if m.Counter.GetValue() != 2 {
		t.Errorf("reallocated_sectors = %v, want 2", m.Counter.GetValue())
	}
	if got := getLabelValue(m, "type"); got != "sat" {
		t.Errorf("type label = %q, want sat", got)
	}

	f = findMetric(families, "station_smart_pending_sectors_total")
	if f == nil || len(f.GetMetric()) == 0 {
		t.Fatal("no pending_sectors metric found")
	}
	if f.GetMetric()[0].Counter.GetValue() != 1 {
		t.Errorf("pending_sectors = %v, want 1", f.GetMetric()[0].Counter.GetValue())
	}
}

// --- Edge Cases ---

func TestSmartCollect_DriveFailsDuringCollection(t *testing.T) {
	scanData := loadFixture(t, "smartctl-scan-mixed.json")
	nvmeData := loadFixture(t, "smartctl-nvme-attrs.json")

	runner := &MockRunner{Responses: map[string]MockResponse{
		"smartctl --scan --json=c":        {Output: scanData},
		"smartctl --json=c -a /dev/nvme0": {Output: nvmeData},
		"smartctl --json=c -a /dev/sda":   {Err: fmt.Errorf("device removed")},
	}}

	sc := NewSmartCollector(runner, 5*time.Minute)
	sc.scanAndCollect()
	families := collectMetrics(t, sc)

	// NVMe should still have metrics
	if v, ok := findMetricValue(families, "station_smart_temperature_celsius"); !ok || v != 29 {
		t.Errorf("NVMe temp should still be present: found=%v val=%v", ok, v)
	}
}

func TestSmartCollect_BeforeFirstScan(t *testing.T) {
	runner := &MockRunner{Responses: map[string]MockResponse{}}
	sc := NewSmartCollector(runner, 5*time.Minute)
	// Don't call scanAndCollect — simulate first scrape before background runs
	families := collectMetrics(t, sc)

	// Should return no metrics, not error
	for _, f := range families {
		if len(f.GetMetric()) > 0 {
			t.Errorf("expected no metrics before first scan, got %s", f.GetName())
		}
	}
}

func TestSmartInterval_MinimumEnforced(t *testing.T) {
	runner := &MockRunner{Responses: map[string]MockResponse{}}
	sc := NewSmartCollector(runner, 10*time.Second) // below minimum
	if sc.interval != minSmartInterval {
		t.Errorf("interval = %v, want %v (minimum enforced)", sc.interval, minSmartInterval)
	}
}

func TestSmartCollect_PartialJSON(t *testing.T) {
	scanData := loadFixture(t, "smartctl-scan-nvme.json")
	// JSON with only temperature, missing all SMART log fields
	partialJSON := []byte(`{"model_name":"Partial","temperature":{"current":40}}`)

	runner := &MockRunner{Responses: map[string]MockResponse{
		"smartctl --scan --json=c":        {Output: scanData},
		"smartctl --json=c -a /dev/nvme0": {Output: partialJSON},
	}}

	sc := NewSmartCollector(runner, 5*time.Minute)
	sc.scanAndCollect()
	families := collectMetrics(t, sc)

	// Temperature should be present
	if v, ok := findMetricValue(families, "station_smart_temperature_celsius"); !ok || v != 40 {
		t.Errorf("temperature = %v (found=%v), want 40", v, ok)
	}

	// Missing fields should simply not be emitted, not cause errors
	if _, ok := findMetricValue(families, "station_smart_available_spare_ratio"); ok {
		t.Error("available_spare should not be present in partial JSON")
	}
}
