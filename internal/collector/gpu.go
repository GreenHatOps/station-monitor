package collector

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const gpuCollectInterval = 15 * time.Second

// GPUInfo holds detected GPU state.
type GPUInfo struct {
	Vendor      string // nvidia, amd, unknown
	Model       string
	Type        string // dgpu, igpu
	DriverReady bool
}

// GPUCollector detects and monitors GPU hardware.
type GPUCollector struct {
	runner CommandRunner

	mu      sync.RWMutex
	gpus    []GPUInfo
	metrics map[int]map[string]float64 // gpu index -> metric -> value

	detectedDesc *prometheus.Desc
	tempDesc     *prometheus.Desc
	utilDesc     *prometheus.Desc
	memUsedDesc  *prometheus.Desc
}

func NewGPUCollector(runner CommandRunner) *GPUCollector {
	labels := []string{"vendor", "device", "model", "type"}
	return &GPUCollector{
		runner:  runner,
		metrics: make(map[int]map[string]float64),
		detectedDesc: prometheus.NewDesc(
			"station_gpu_detected", "GPU detected (1=yes)",
			append(labels, "driver"), nil,
		),
		tempDesc: prometheus.NewDesc(
			"station_gpu_temperature_celsius", "GPU temperature",
			labels, nil,
		),
		utilDesc: prometheus.NewDesc(
			"station_gpu_utilization_ratio", "GPU utilization (0-1)",
			labels, nil,
		),
		memUsedDesc: prometheus.NewDesc(
			"station_gpu_memory_used_bytes", "GPU memory used",
			labels, nil,
		),
	}
}

func (c *GPUCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.detectedDesc
	ch <- c.tempDesc
	ch <- c.utilDesc
	ch <- c.memUsedDesc
}

func (c *GPUCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for i, gpu := range c.gpus {
		device := "gpu" + strconv.Itoa(i)
		driver := "missing"
		if gpu.DriverReady {
			driver = "loaded"
		}
		lv := []string{gpu.Vendor, device, gpu.Model, gpu.Type}

		ch <- prometheus.MustNewConstMetric(c.detectedDesc, prometheus.GaugeValue, 1,
			append(lv, driver)...)

		if m, ok := c.metrics[i]; ok {
			if v, ok := m["temperature"]; ok {
				ch <- prometheus.MustNewConstMetric(c.tempDesc, prometheus.GaugeValue, v, lv...)
			}
			if v, ok := m["utilization"]; ok {
				ch <- prometheus.MustNewConstMetric(c.utilDesc, prometheus.GaugeValue, v/100.0, lv...)
			}
			if v, ok := m["memory_used"]; ok {
				// nvidia-smi reports in MiB
				ch <- prometheus.MustNewConstMetric(c.memUsedDesc, prometheus.GaugeValue, v*1024*1024, lv...)
			}
		}
	}
}

// StartBackground probes for GPUs then collects metrics in a loop.
func (c *GPUCollector) StartBackground() {
	c.probe()
	c.collectMetrics()
	ticker := time.NewTicker(gpuCollectInterval)
	for range ticker.C {
		c.collectMetrics()
	}
}

func (c *GPUCollector) probe() {
	var gpus []GPUInfo

	// Check NVIDIA
	if hasNvidiaDevice() {
		gpu := GPUInfo{Vendor: "nvidia", Type: "dgpu"}
		out, err := ExecWithTimeout(c.runner, 10*time.Second, "nvidia-smi",
			"--query-gpu=name", "--format=csv,noheader,nounits")
		if err == nil {
			gpu.Model = strings.TrimSpace(string(out))
			gpu.DriverReady = true
		}
		gpus = append(gpus, gpu)
	}

	// Check AMD via sysfs
	amdGPUs := detectAMDGPUs()
	for _, ag := range amdGPUs {
		gpu := GPUInfo{Vendor: "amd", Model: ag.model, Type: ag.gpuType}
		// Check if rocm-smi is available
		_, err := ExecWithTimeout(c.runner, 5*time.Second, "rocm-smi", "--showid")
		if err == nil {
			gpu.DriverReady = true
		}
		gpus = append(gpus, gpu)
	}

	// Fallback: check sysfs vendor IDs if nothing found yet
	if len(gpus) == 0 {
		gpus = detectGPUViaSysfs(gpus)
	}

	c.mu.Lock()
	c.gpus = gpus
	c.mu.Unlock()

	if len(gpus) > 0 {
		for _, g := range gpus {
			log.Printf("GPU detected: %s %s (%s, driver=%v)", g.Vendor, g.Model, g.Type, g.DriverReady)
		}
	} else {
		log.Printf("No GPU detected")
	}
}

func (c *GPUCollector) collectMetrics() {
	c.mu.RLock()
	gpus := c.gpus
	c.mu.RUnlock()

	metrics := make(map[int]map[string]float64)

	for i, gpu := range gpus {
		if !gpu.DriverReady {
			continue
		}
		switch gpu.Vendor {
		case "nvidia":
			m := c.collectNvidia()
			if m != nil {
				metrics[i] = m
			}
		}
		// AMD ROCm collection can be added when rocm-smi JSON is stable
	}

	c.mu.Lock()
	c.metrics = metrics
	c.mu.Unlock()
}

func (c *GPUCollector) collectNvidia() map[string]float64 {
	out, err := ExecWithTimeout(c.runner, 10*time.Second, "nvidia-smi",
		"--query-gpu=temperature.gpu,utilization.gpu,memory.used",
		"--format=csv,noheader,nounits")
	if err != nil {
		return nil
	}

	fields := strings.Split(strings.TrimSpace(string(out)), ", ")
	if len(fields) < 3 {
		return nil
	}

	m := make(map[string]float64)
	if v, err := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64); err == nil {
		m["temperature"] = v
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64); err == nil {
		m["utilization"] = v
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(fields[2]), 64); err == nil {
		m["memory_used"] = v
	}
	return m
}

// GPUs returns detected GPUs (used by inventory).
func (c *GPUCollector) GPUs() []GPUInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]GPUInfo, len(c.gpus))
	copy(result, c.gpus)
	return result
}

// --- detection helpers ---

func hasNvidiaDevice() bool {
	matches, _ := filepath.Glob("/dev/nvidia[0-9]*")
	return len(matches) > 0
}

type amdGPU struct {
	model   string
	gpuType string // igpu or dgpu
}

func detectAMDGPUs() []amdGPU {
	var gpus []amdGPU
	cards, _ := filepath.Glob("/sys/class/drm/card[0-9]*/device/vendor")
	for _, vendorPath := range cards {
		data, err := os.ReadFile(vendorPath)
		if err != nil {
			continue
		}
		vendor := strings.TrimSpace(string(data))
		if vendor != "0x1002" { // AMD
			continue
		}
		dir := filepath.Dir(vendorPath)
		model := readSysfsString(filepath.Join(dir, "product_name"))
		if model == "" {
			model = "AMD GPU"
		}

		// Heuristic: if boot_vga=1 and no dedicated VRAM file, likely iGPU
		gpuType := "dgpu"
		bootVGA := readSysfsString(filepath.Join(dir, "boot_vga"))
		if bootVGA == "1" {
			gpuType = "igpu"
		}

		gpus = append(gpus, amdGPU{model: model, gpuType: gpuType})
	}
	return gpus
}

func detectGPUViaSysfs(existing []GPUInfo) []GPUInfo {
	cards, _ := filepath.Glob("/sys/class/drm/card[0-9]*/device/vendor")
	for _, vendorPath := range cards {
		data, err := os.ReadFile(vendorPath)
		if err != nil {
			continue
		}
		vendor := strings.TrimSpace(string(data))
		var gpu GPUInfo
		switch vendor {
		case "0x10de":
			gpu = GPUInfo{Vendor: "nvidia", Type: "dgpu"}
		case "0x1002":
			gpu = GPUInfo{Vendor: "amd", Type: "dgpu"}
		default:
			continue
		}
		dir := filepath.Dir(vendorPath)
		gpu.Model = readSysfsString(filepath.Join(dir, "product_name"))
		if gpu.Model == "" {
			gpu.Model = "Unknown " + gpu.Vendor + " GPU"
		}
		existing = append(existing, gpu)
	}
	return existing
}

func readSysfsString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
