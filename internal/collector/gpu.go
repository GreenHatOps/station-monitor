package collector

import (
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	gpuCollectInterval = 15 * time.Second
	// Re-probe every 20 collect ticks (~5m) so drivers loaded or GPUs added
	// after startup are picked up without a restart.
	gpuReprobeTicks = 20
)

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
	probed  bool
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

// StartBackground probes for GPUs then collects metrics in a loop,
// re-probing periodically to catch hardware or driver changes.
func (c *GPUCollector) StartBackground() {
	c.probe()
	c.collectMetrics()
	ticker := time.NewTicker(gpuCollectInterval)
	ticks := 0
	for range ticker.C {
		ticks++
		if ticks%gpuReprobeTicks == 0 {
			c.probe()
		}
		c.collectMetrics()
	}
}

func (c *GPUCollector) probe() {
	var gpus []GPUInfo

	// Check NVIDIA: one entry per nvidia-smi line (multi-GPU)
	if hasNvidiaDevice() {
		out, err := ExecWithTimeout(c.runner, 10*time.Second, "nvidia-smi",
			"--query-gpu=name", "--format=csv,noheader,nounits")
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				name := strings.TrimSpace(line)
				if name == "" {
					continue
				}
				gpus = append(gpus, GPUInfo{Vendor: "nvidia", Model: name, Type: "dgpu", DriverReady: true})
			}
		}
		if len(gpus) == 0 {
			// Device node exists but nvidia-smi failed: driver not usable
			gpus = append(gpus, GPUInfo{Vendor: "nvidia", Type: "dgpu"})
		}
	}

	// AMD: a card under /sys/class/drm means the kernel driver is bound
	for _, ag := range detectAMDGPUs() {
		gpus = append(gpus, GPUInfo{Vendor: "amd", Model: ag.model, Type: ag.gpuType, DriverReady: true})
	}

	// Fallback: scan the PCI bus, which also finds GPUs with no driver bound
	if len(gpus) == 0 {
		gpus = detectGPUViaPCI(gpus)
	}

	c.mu.Lock()
	changed := !c.probed || !reflect.DeepEqual(c.gpus, gpus)
	c.gpus = gpus
	c.probed = true
	c.mu.Unlock()

	if !changed {
		return
	}
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
	gpus := make([]GPUInfo, len(c.gpus))
	copy(gpus, c.gpus)
	c.mu.RUnlock()

	metrics := make(map[int]map[string]float64)

	// nvidia-smi reports all NVIDIA GPUs in one call, one line per GPU,
	// in the same order they were appended during probe.
	var nvidiaMaps []map[string]float64
	nvidiaQueried := false
	nvidiaIdx := 0

	for i, gpu := range gpus {
		if !gpu.DriverReady {
			continue
		}
		switch gpu.Vendor {
		case "nvidia":
			if !nvidiaQueried {
				nvidiaMaps = c.collectNvidia()
				nvidiaQueried = true
			}
			if nvidiaIdx < len(nvidiaMaps) && nvidiaMaps[nvidiaIdx] != nil {
				metrics[i] = nvidiaMaps[nvidiaIdx]
			}
			nvidiaIdx++
		}
		// AMD ROCm collection can be added when rocm-smi JSON is stable
	}

	c.mu.Lock()
	c.metrics = metrics
	c.mu.Unlock()
}

// collectNvidia returns one metric map per nvidia-smi output line;
// malformed lines yield nil entries to keep index alignment.
func (c *GPUCollector) collectNvidia() []map[string]float64 {
	out, err := ExecWithTimeout(c.runner, 10*time.Second, "nvidia-smi",
		"--query-gpu=temperature.gpu,utilization.gpu,memory.used",
		"--format=csv,noheader,nounits")
	if err != nil {
		return nil
	}

	var result []map[string]float64
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(strings.TrimSpace(line), ", ")
		if len(fields) < 3 {
			result = append(result, nil)
			continue
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
		if len(m) == 0 {
			result = append(result, nil)
			continue
		}
		result = append(result, m)
	}
	return result
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

// detectGPUViaPCI scans the PCI bus directly, so it also finds display
// controllers that have no kernel driver bound (invisible in /sys/class/drm).
func detectGPUViaPCI(existing []GPUInfo) []GPUInfo {
	classes, _ := filepath.Glob("/sys/bus/pci/devices/*/class")
	for _, classPath := range classes {
		if !strings.HasPrefix(readSysfsString(classPath), "0x03") {
			continue // 0x03xxxx = display controller
		}
		dir := filepath.Dir(classPath)
		var gpu GPUInfo
		switch readSysfsString(filepath.Join(dir, "vendor")) {
		case "0x10de":
			gpu = GPUInfo{Vendor: "nvidia", Type: "dgpu"}
		case "0x1002":
			gpu = GPUInfo{Vendor: "amd", Type: "dgpu"}
		default:
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "driver")); err == nil {
			gpu.DriverReady = true
		}
		gpu.Model = "Unknown " + gpu.Vendor + " GPU"
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
