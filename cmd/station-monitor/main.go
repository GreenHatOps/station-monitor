package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/GreenHatOps/station-monitor/internal/collector"
	"github.com/GreenHatOps/station-monitor/internal/inventory"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1"
	}
	port := os.Getenv("LISTEN_PORT")
	if port == "" {
		port = "9101"
	}

	smartInterval := 5 * time.Minute
	if v := os.Getenv("SMART_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			smartInterval = d
		}
	}

	runner := &collector.RealRunner{}
	reg := prometheus.NewRegistry()

	// SMART collector (auto-detects NVMe + SATA drives)
	smartCollector := collector.NewSmartCollector(runner, smartInterval)
	reg.MustRegister(smartCollector)
	go smartCollector.StartBackground()

	// GPU collector (auto-detects NVIDIA/AMD)
	gpuCollector := collector.NewGPUCollector(runner)
	reg.MustRegister(gpuCollector)
	go gpuCollector.StartBackground()

	// Inventory endpoint (reads from collector caches)
	inv := &inventory.Provider{
		Smart: smartCollector,
		GPU:   gpuCollector,
	}

	http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	http.HandleFunc("/inventory", inv.Handler())
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	listen := addr + ":" + port
	log.Printf("station-monitor v0.1.0 listening on %s", listen)
	if err := http.ListenAndServe(listen, nil); err != nil {
		log.Fatal(err)
	}
}
