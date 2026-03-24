package inventory

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/GreenHatOps/station-monitor/internal/collector"
)

const version = "0.1.0"

// Provider gives inventory access to collector state.
type Provider struct {
	Smart *collector.SmartCollector
	GPU   *collector.GPUCollector
}

type Inventory struct {
	Hostname string    `json:"hostname"`
	OS       string    `json:"os"`
	Kernel   string    `json:"kernel"`
	Arch     string    `json:"arch"`
	CPU      CPUInfo   `json:"cpu"`
	MemBytes uint64    `json:"memory_bytes"`
	Drives   []DriveInfo `json:"drives"`
	GPUs     []GPUInfo `json:"gpu"`
	Version  string    `json:"station_monitor_version"`
}

type CPUInfo struct {
	Model   string `json:"model"`
	Cores   int    `json:"cores"`
	Threads int    `json:"threads"`
}

type DriveInfo struct {
	Device string `json:"device"`
	Model  string `json:"model"`
	Type   string `json:"type"`
}

type GPUInfo struct {
	Vendor      string `json:"vendor"`
	Model       string `json:"model"`
	Type        string `json:"type"`
	DriverReady bool   `json:"driver_loaded"`
}

func (p *Provider) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inv := Inventory{
			Hostname: readProcString("/host/proc/sys/kernel/hostname"),
			Kernel:   readProcString("/host/proc/sys/kernel/osrelease"),
			OS:       readOSName(),
			Arch:     runtime.GOARCH,
			CPU:      readCPUInfo(),
			MemBytes: readMemTotal(),
			Version:  version,
		}

		// Drives from SMART collector cache
		if p.Smart != nil {
			for _, d := range p.Smart.Drives() {
				inv.Drives = append(inv.Drives, DriveInfo{
					Device: d.Device,
					Model:  d.Model,
					Type:   d.Type,
				})
			}
		}

		// GPUs from GPU collector cache
		if p.GPU != nil {
			for _, g := range p.GPU.GPUs() {
				inv.GPUs = append(inv.GPUs, GPUInfo{
					Vendor:      g.Vendor,
					Model:       g.Model,
					Type:        g.Type,
					DriverReady: g.DriverReady,
				})
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(inv)
	}
}

func readProcString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readOSName() string {
	data, err := os.ReadFile("/host/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
		}
	}
	return ""
}

func readCPUInfo() CPUInfo {
	data, err := os.ReadFile("/host/proc/cpuinfo")
	if err != nil {
		return CPUInfo{}
	}

	var info CPUInfo
	cores := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "model name":
			info.Model = val
		case "core id":
			cores[val] = true
		case "processor":
			info.Threads++
		}
	}
	info.Cores = len(cores)
	if info.Cores == 0 {
		info.Cores = info.Threads
	}
	return info
}

func readMemTotal() uint64 {
	data, err := os.ReadFile("/host/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseUint(fields[1], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}
