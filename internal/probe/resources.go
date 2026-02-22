package probe

import "runtime"

// ResourcesBlock holds process resource metrics.
type ResourcesBlock struct {
	MemAllocMB   float64 `json:"mem_alloc_mb"`
	MemSysMB     float64 `json:"mem_sys_mb"`
	MemHeapInuse float64 `json:"mem_heap_inuse_mb"`
	Goroutines   int     `json:"goroutines"`
	OpenFDs      int     `json:"open_fds"` // -1 if unavailable
	MaxFDs       int     `json:"max_fds"`  // -1 if unavailable
}

// WatermarksBlock holds peak throughput values since process startup.
type WatermarksBlock struct {
	PeakReqPerSec  float64 `json:"peak_req_per_sec"`
	PeakBytesInSec int64   `json:"peak_bytes_in_sec"`
}

const bytesPerMB = 1024 * 1024

// collectResources gathers current process resource metrics.
func collectResources() ResourcesBlock {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return ResourcesBlock{
		MemAllocMB:   float64(m.Alloc) / bytesPerMB,
		MemSysMB:     float64(m.Sys) / bytesPerMB,
		MemHeapInuse: float64(m.HeapInuse) / bytesPerMB,
		Goroutines:   runtime.NumGoroutine(),
		OpenFDs:      countOpenFDs(),
		MaxFDs:       getMaxFDs(),
	}
}
