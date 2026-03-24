package collector

import (
	"math"
	"testing"
)

func TestGPUCollector_NoGPU(t *testing.T) {
	runner := &MockRunner{Responses: map[string]MockResponse{}}
	gc := NewGPUCollector(runner)
	// No /dev/nvidia*, no sysfs — probe finds nothing
	// Note: probe() checks real /dev and /sys, so in test env it won't find GPUs
	gc.mu.Lock()
	gc.gpus = nil // force no GPUs
	gc.mu.Unlock()

	families := collectMetrics(t, gc)
	f := findMetric(families, "station_gpu_detected")
	if f != nil && len(f.GetMetric()) > 0 {
		t.Error("expected no GPU metrics when no GPU detected")
	}
}

func TestGPUCollector_NvidiaWithDriver(t *testing.T) {
	runner := &MockRunner{Responses: map[string]MockResponse{
		"nvidia-smi --query-gpu=temperature.gpu,utilization.gpu,memory.used --format=csv,noheader,nounits": {
			Output: []byte("65, 87, 18432\n"),
		},
	}}

	gc := NewGPUCollector(runner)
	// Manually set detected GPU state
	gc.mu.Lock()
	gc.gpus = []GPUInfo{
		{Vendor: "nvidia", Model: "NVIDIA GeForce RTX 4090", Type: "dgpu", DriverReady: true},
	}
	gc.mu.Unlock()

	gc.collectMetrics()
	families := collectMetrics(t, gc)

	// station_gpu_detected should be 1
	f := findMetric(families, "station_gpu_detected")
	if f == nil || len(f.GetMetric()) == 0 {
		t.Fatal("station_gpu_detected not found")
	}
	m := f.GetMetric()[0]
	if m.Gauge.GetValue() != 1 {
		t.Errorf("gpu_detected = %v, want 1", m.Gauge.GetValue())
	}
	if got := getLabelValue(m, "vendor"); got != "nvidia" {
		t.Errorf("vendor = %q, want nvidia", got)
	}
	if got := getLabelValue(m, "driver"); got != "loaded" {
		t.Errorf("driver = %q, want loaded", got)
	}
	if got := getLabelValue(m, "type"); got != "dgpu" {
		t.Errorf("type = %q, want dgpu", got)
	}

	// Temperature
	temp, ok := findMetricValue(families, "station_gpu_temperature_celsius")
	if !ok || temp != 65 {
		t.Errorf("gpu_temperature = %v (found=%v), want 65", temp, ok)
	}

	// Utilization (87% -> 0.87)
	util, ok := findMetricValue(families, "station_gpu_utilization_ratio")
	if !ok || math.Abs(util-0.87) > 0.001 {
		t.Errorf("gpu_utilization = %v (found=%v), want 0.87", util, ok)
	}

	// Memory (18432 MiB -> bytes)
	mem, ok := findMetricValue(families, "station_gpu_memory_used_bytes")
	if !ok || mem != 18432*1024*1024 {
		t.Errorf("gpu_memory = %v (found=%v), want %v", mem, ok, 18432*1024*1024)
	}
}

func TestGPUCollector_NvidiaDriverMissing(t *testing.T) {
	runner := &MockRunner{Responses: map[string]MockResponse{}}
	gc := NewGPUCollector(runner)

	gc.mu.Lock()
	gc.gpus = []GPUInfo{
		{Vendor: "nvidia", Model: "Unknown GPU", Type: "dgpu", DriverReady: false},
	}
	gc.mu.Unlock()

	gc.collectMetrics()
	families := collectMetrics(t, gc)

	// Should have detected metric but no utilization metrics
	f := findMetric(families, "station_gpu_detected")
	if f == nil || len(f.GetMetric()) == 0 {
		t.Fatal("station_gpu_detected should be present even without driver")
	}
	m := f.GetMetric()[0]
	if got := getLabelValue(m, "driver"); got != "missing" {
		t.Errorf("driver = %q, want missing", got)
	}

	// No temperature/utilization metrics
	if _, ok := findMetricValue(families, "station_gpu_temperature_celsius"); ok {
		t.Error("should not have temperature without driver")
	}
	if _, ok := findMetricValue(families, "station_gpu_utilization_ratio"); ok {
		t.Error("should not have utilization without driver")
	}
}

func TestGPUCollector_NvidiaMalformedOutput(t *testing.T) {
	runner := &MockRunner{Responses: map[string]MockResponse{
		"nvidia-smi --query-gpu=temperature.gpu,utilization.gpu,memory.used --format=csv,noheader,nounits": {
			Output: []byte("garbage output\n"),
		},
	}}

	gc := NewGPUCollector(runner)
	gc.mu.Lock()
	gc.gpus = []GPUInfo{
		{Vendor: "nvidia", Model: "RTX 4090", Type: "dgpu", DriverReady: true},
	}
	gc.mu.Unlock()

	gc.collectMetrics()
	families := collectMetrics(t, gc)

	// Detected, but no utilization metrics (parsing failed)
	f := findMetric(families, "station_gpu_detected")
	if f == nil || len(f.GetMetric()) == 0 {
		t.Fatal("gpu_detected should still be present")
	}
	if _, ok := findMetricValue(families, "station_gpu_temperature_celsius"); ok {
		t.Error("should not have temperature from malformed output")
	}
}

func TestGPUCollector_AMDiGPU(t *testing.T) {
	runner := &MockRunner{Responses: map[string]MockResponse{}}
	gc := NewGPUCollector(runner)

	gc.mu.Lock()
	gc.gpus = []GPUInfo{
		{Vendor: "amd", Model: "Raphael (iGPU)", Type: "igpu", DriverReady: false},
	}
	gc.mu.Unlock()

	gc.collectMetrics()
	families := collectMetrics(t, gc)

	f := findMetric(families, "station_gpu_detected")
	if f == nil || len(f.GetMetric()) == 0 {
		t.Fatal("gpu_detected should be present for AMD iGPU")
	}
	m := f.GetMetric()[0]
	if got := getLabelValue(m, "type"); got != "igpu" {
		t.Errorf("type = %q, want igpu", got)
	}
	if got := getLabelValue(m, "vendor"); got != "amd" {
		t.Errorf("vendor = %q, want amd", got)
	}
}
