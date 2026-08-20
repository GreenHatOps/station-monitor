package collector

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/tidwall/gjson"
)

const (
	smartTimeout    = 30 * time.Second
	minSmartInterval = 1 * time.Minute
)

// Drive represents a detected storage device.
type Drive struct {
	Device string // e.g. /dev/nvme0n1
	Name   string // short name, e.g. nvme0n1
	Type   string // nvme, sat, scsi
	Model  string
}

// SmartCollector auto-detects drives and collects SMART metrics.
type SmartCollector struct {
	runner   CommandRunner
	interval time.Duration

	mu      sync.RWMutex
	drives  []Drive
	metrics map[string]map[string]float64 // device -> metric -> value

	// Metric descriptors
	tempDesc       *prometheus.Desc
	spareDesc      *prometheus.Desc
	spareThresh    *prometheus.Desc
	usedDesc       *prometheus.Desc
	powerOnDesc    *prometheus.Desc
	powerCycleDesc *prometheus.Desc
	unsafeShutDesc *prometheus.Desc
	mediaErrDesc   *prometheus.Desc
	critWarnDesc   *prometheus.Desc
	readDesc       *prometheus.Desc
	writtenDesc    *prometheus.Desc
	reallocDesc    *prometheus.Desc
	pendingDesc    *prometheus.Desc
}

func NewSmartCollector(runner CommandRunner, interval time.Duration) *SmartCollector {
	if interval < minSmartInterval {
		interval = minSmartInterval
	}
	labels := []string{"device", "model", "type"}
	return &SmartCollector{
		runner:   runner,
		interval: interval,
		metrics:  make(map[string]map[string]float64),
		tempDesc: prometheus.NewDesc(
			"station_smart_temperature_celsius", "Drive temperature", labels, nil,
		),
		spareDesc: prometheus.NewDesc(
			"station_smart_available_spare_ratio", "Available spare (0-1, NVMe only)", labels, nil,
		),
		spareThresh: prometheus.NewDesc(
			"station_smart_available_spare_threshold_ratio", "Spare threshold (0-1, NVMe only)", labels, nil,
		),
		usedDesc: prometheus.NewDesc(
			"station_smart_percentage_used_ratio", "Life used (0-1+, NVMe only)", labels, nil,
		),
		powerOnDesc: prometheus.NewDesc(
			"station_smart_power_on_hours_total", "Power-on hours", labels, nil,
		),
		powerCycleDesc: prometheus.NewDesc(
			"station_smart_power_cycles_total", "Power cycles", labels, nil,
		),
		unsafeShutDesc: prometheus.NewDesc(
			"station_smart_unsafe_shutdowns_total", "Unsafe shutdowns (NVMe only)", labels, nil,
		),
		mediaErrDesc: prometheus.NewDesc(
			"station_smart_media_errors_total", "Media errors (NVMe only)", labels, nil,
		),
		critWarnDesc: prometheus.NewDesc(
			"station_smart_critical_warning", "Critical warning bitmask (NVMe only, 0=healthy)", labels, nil,
		),
		readDesc: prometheus.NewDesc(
			"station_smart_data_read_bytes_total", "Total bytes read", labels, nil,
		),
		writtenDesc: prometheus.NewDesc(
			"station_smart_data_written_bytes_total", "Total bytes written", labels, nil,
		),
		reallocDesc: prometheus.NewDesc(
			"station_smart_reallocated_sectors_total", "Reallocated sectors (SATA only)", labels, nil,
		),
		pendingDesc: prometheus.NewDesc(
			"station_smart_pending_sectors_total", "Pending sectors (SATA only)", labels, nil,
		),
	}
}

func (c *SmartCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.tempDesc
	ch <- c.spareDesc
	ch <- c.spareThresh
	ch <- c.usedDesc
	ch <- c.powerOnDesc
	ch <- c.powerCycleDesc
	ch <- c.unsafeShutDesc
	ch <- c.mediaErrDesc
	ch <- c.critWarnDesc
	ch <- c.readDesc
	ch <- c.writtenDesc
	ch <- c.reallocDesc
	ch <- c.pendingDesc
}

func (c *SmartCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, drive := range c.drives {
		m, ok := c.metrics[drive.Name]
		if !ok {
			continue
		}
		lv := []string{drive.Name, drive.Model, drive.Type}

		emit := func(desc *prometheus.Desc, key string) {
			if val, ok := m[key]; ok {
				ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, val, lv...)
			}
		}
		emitCounter := func(desc *prometheus.Desc, key string) {
			if val, ok := m[key]; ok {
				ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, val, lv...)
			}
		}

		emit(c.tempDesc, "temperature")
		emit(c.critWarnDesc, "critical_warning")

		if drive.Type == "nvme" {
			emit(c.spareDesc, "available_spare")
			emit(c.spareThresh, "available_spare_threshold")
			emit(c.usedDesc, "percentage_used")
			emitCounter(c.unsafeShutDesc, "unsafe_shutdowns")
			emitCounter(c.mediaErrDesc, "media_errors")
		} else {
			emitCounter(c.reallocDesc, "reallocated_sectors")
			emitCounter(c.pendingDesc, "pending_sectors")
		}

		emitCounter(c.powerOnDesc, "power_on_hours")
		emitCounter(c.powerCycleDesc, "power_cycles")
		emitCounter(c.readDesc, "data_read_bytes")
		emitCounter(c.writtenDesc, "data_written_bytes")
	}
}

// StartBackground runs drive discovery and SMART collection in a loop.
func (c *SmartCollector) StartBackground() {
	c.scanAndCollect()
	ticker := time.NewTicker(c.interval)
	for range ticker.C {
		c.scanAndCollect()
	}
}

func (c *SmartCollector) scanAndCollect() {
	drives := c.discoverDrives()
	metrics := make(map[string]map[string]float64)

	for i := range drives {
		m := c.collectDrive(&drives[i])
		if m != nil {
			metrics[drives[i].Name] = m
		}
	}

	c.mu.Lock()
	c.drives = drives
	c.metrics = metrics
	c.mu.Unlock()
}

func (c *SmartCollector) discoverDrives() []Drive {
	out, err := ExecWithTimeout(c.runner, smartTimeout, "smartctl", "--scan", "--json=c")
	if err != nil {
		log.Printf("smartctl --scan error: %v", err)
		return nil
	}

	var drives []Drive
	devices := gjson.Get(string(out), "devices")
	devices.ForEach(func(_, dev gjson.Result) bool {
		device := dev.Get("name").String()
		devType := dev.Get("type").String()
		if devType == "sat" || devType == "scsi" {
			devType = "sat"
		}
		name := device
		if idx := strings.LastIndex(device, "/"); idx >= 0 {
			name = device[idx+1:]
		}
		drives = append(drives, Drive{
			Device: device,
			Name:   name,
			Type:   devType,
		})
		return true
	})
	return drives
}

func (c *SmartCollector) collectDrive(drive *Drive) map[string]float64 {
	out, err := ExecWithTimeout(c.runner, smartTimeout, "smartctl", "--json=c", "-a", drive.Device)
	if err != nil {
		log.Printf("smartctl %s error: %v", drive.Device, err)
		return nil
	}

	json := string(out)
	m := make(map[string]float64)

	// Update drive model from full output
	if model := gjson.Get(json, "model_name").String(); model != "" {
		drive.Model = model
	}

	// Temperature (common to NVMe and SATA)
	if temp := gjson.Get(json, "temperature.current"); temp.Exists() {
		m["temperature"] = temp.Float()
	}

	if drive.Type == "nvme" {
		c.parseNVMe(json, m)
	} else {
		c.parseSATA(json, m)
	}

	// Power-on hours (common path)
	if h := gjson.Get(json, "power_on_time.hours"); h.Exists() {
		m["power_on_hours"] = h.Float()
	}

	// Power cycles
	if pc := gjson.Get(json, "power_cycle_count"); pc.Exists() {
		m["power_cycles"] = pc.Float()
	}

	return m
}

func (c *SmartCollector) parseNVMe(json string, m map[string]float64) {
	base := "nvme_smart_health_information_log."

	if v := gjson.Get(json, base+"available_spare"); v.Exists() {
		m["available_spare"] = v.Float() / 100.0 // normalize 0-100 → 0.0-1.0
	}
	if v := gjson.Get(json, base+"available_spare_threshold"); v.Exists() {
		m["available_spare_threshold"] = v.Float() / 100.0
	}
	if v := gjson.Get(json, base+"percentage_used"); v.Exists() {
		m["percentage_used"] = v.Float() / 100.0
	}
	if v := gjson.Get(json, base+"power_cycles"); v.Exists() {
		m["power_cycles"] = v.Float()
	}
	if v := gjson.Get(json, base+"unsafe_shutdowns"); v.Exists() {
		m["unsafe_shutdowns"] = v.Float()
	}
	if v := gjson.Get(json, base+"media_errors"); v.Exists() {
		m["media_errors"] = v.Float()
	}
	if v := gjson.Get(json, base+"critical_warning"); v.Exists() {
		m["critical_warning"] = v.Float()
	}
	// Data units: each unit = 512000 bytes (1000 × 512)
	if v := gjson.Get(json, base+"data_units_read"); v.Exists() {
		m["data_read_bytes"] = v.Float() * 512000
	}
	if v := gjson.Get(json, base+"data_units_written"); v.Exists() {
		m["data_written_bytes"] = v.Float() * 512000
	}
}

func (c *SmartCollector) parseSATA(json string, m map[string]float64) {
	// SATA attributes by ID
	attrs := gjson.Get(json, "ata_smart_attributes.table")
	attrs.ForEach(func(_, attr gjson.Result) bool {
		id := attr.Get("id").Int()
		raw := attr.Get("raw.value").Float()
		switch id {
		case 5: // Reallocated_Sector_Ct
			m["reallocated_sectors"] = raw
		case 9: // Power_On_Hours
			m["power_on_hours"] = raw
		case 12: // Power_Cycle_Count
			m["power_cycles"] = raw
		case 197: // Current_Pending_Sector
			m["pending_sectors"] = raw
		case 241: // Total_LBAs_Written (assumes 512-byte sectors)
			m["data_written_bytes"] = raw * 512
		case 242: // Total_LBAs_Read (assumes 512-byte sectors)
			m["data_read_bytes"] = raw * 512
		}
		return true
	})
}

// Drives returns the currently detected drives (used by inventory).
func (c *SmartCollector) Drives() []Drive {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]Drive, len(c.drives))
	copy(result, c.drives)
	return result
}
