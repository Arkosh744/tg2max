package admin

import (
	"encoding/json"
	"net/http"
	"runtime"
	"syscall"
)

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	var diskFree uint64
	var disk syscall.Statfs_t
	if err := syscall.Statfs("/tmp", &disk); err == nil {
		diskFree = disk.Bavail * uint64(disk.Bsize)
	}

	stats, _ := s.store.GetStats(r.Context())
	live := s.bot.ActiveMigration()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"goroutines":       runtime.NumGoroutine(),
		"heap_alloc_mb":    mem.HeapAlloc / 1024 / 1024,
		"heap_sys_mb":      mem.HeapSys / 1024 / 1024,
		"disk_free_mb":     diskFree / 1024 / 1024,
		"uptime":           s.fmtUptime(),
		"total_users":      stats.TotalUsers,
		"total_migrations": stats.TotalMigrations,
		"completed":        stats.Completed,
		"failed":           stats.Failed,
		"cancelled":        stats.Cancelled,
		"total_sent":       stats.TotalSent,
		"active_migration": live != nil,
	})
}
