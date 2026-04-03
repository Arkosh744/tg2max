package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/arkosh/tg2max/internal/storage"
)

type pageData struct {
	Title      string
	Active     string // nav highlight: "dashboard", "migrations", "users"
	Uptime     string
	Stats      storage.UserStats
	Migration  *storage.Migration
	Migrations []storage.Migration
	Users      []storage.UserRow
	User       *storage.User
	Page       int
	TotalPages int
	Total      int
	Status     string // current filter
	LiveMig    interface{} // *models.LiveMigration
	Recent     []storage.Migration
	History    []storage.HistoryEntry
	ChartJSON  string // JSON array of daily stats for Chart.js
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/" {
		http.NotFound(w, r)
		return
	}
	stats, err := s.store.GetStats(r.Context())
	if err != nil {
		s.log.Error("dashboard: get stats failed", "error", err)
	}
	recent, err := s.store.GetRecentMigrations(r.Context(), 5)
	if err != nil {
		s.log.Error("dashboard: get recent migrations failed", "error", err)
	}

	chartJSON := "[]"
	if daily, err := s.store.GetDailyStats(r.Context(), 30); err != nil {
		s.log.Error("dashboard: get daily stats failed", "error", err)
	} else if len(daily) > 0 {
		if data, err := json.Marshal(daily); err == nil {
			chartJSON = string(data)
		}
	}

	s.render(w, "dashboard.html", pageData{
		Title:     "Dashboard",
		Active:    "dashboard",
		Uptime:    s.fmtUptime(),
		Stats:     stats,
		Recent:    recent,
		LiveMig:   s.bot.ActiveMigration(),
		ChartJSON: chartJSON,
	})
}

func (s *Server) handleMigrations(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	status := r.URL.Query().Get("status")

	migrations, total, err := s.store.ListMigrations(r.Context(), storage.MigrationFilter{
		Status:  status,
		Page:    page,
		PerPage: 20,
	})
	if err != nil {
		s.log.Error("list migrations failed", "error", err)
		http.Error(w, "Ошибка загрузки миграций", http.StatusInternalServerError)
		return
	}

	totalPages := (total + 19) / 20

	s.render(w, "migrations.html", pageData{
		Title:      "Миграции",
		Active:     "migrations",
		Uptime:     s.fmtUptime(),
		Migrations: migrations,
		Page:       page,
		TotalPages: totalPages,
		Total:      total,
		Status:     status,
	})
}

func (s *Server) handleMigrationDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	m, err := s.store.GetMigration(r.Context(), id)
	if err != nil {
		s.log.Error("get migration failed", "id", id, "error", err)
		http.Error(w, "Ошибка загрузки миграции", http.StatusInternalServerError)
		return
	}
	if m == nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, "migration_detail.html", pageData{
		Title:     "Миграция #" + strconv.FormatInt(id, 10),
		Active:    "migrations",
		Uptime:    s.fmtUptime(),
		Migration: m,
	})
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	users, total, err := s.store.ListUsers(r.Context(), page, 20)
	if err != nil {
		s.log.Error("list users failed", "error", err)
		http.Error(w, "Ошибка загрузки пользователей", http.StatusInternalServerError)
		return
	}
	totalPages := (total + 19) / 20

	s.render(w, "users.html", pageData{
		Title:      "Пользователи",
		Active:     "users",
		Uptime:     s.fmtUptime(),
		Users:      users,
		Page:       page,
		TotalPages: totalPages,
		Total:      total,
	})
}

func (s *Server) handleUserDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u, err := s.store.GetUser(r.Context(), id)
	if err != nil {
		s.log.Error("get user failed", "id", id, "error", err)
		http.Error(w, "Ошибка загрузки пользователя", http.StatusInternalServerError)
		return
	}
	if u == nil {
		http.NotFound(w, r)
		return
	}
	history, err := s.store.GetUserHistory(r.Context(), id, 50)
	if err != nil {
		s.log.Error("get user history failed", "id", id, "error", err)
	}

	s.render(w, "user_detail.html", pageData{
		Title:   u.FirstName + " " + u.LastName,
		Active:  "users",
		Uptime:  s.fmtUptime(),
		User:    u,
		History: history,
	})
}

// --- HTMX Partials ---

func (s *Server) handlePartialStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.GetStats(r.Context())
	if err != nil {
		s.log.Error("partial stats failed", "error", err)
	}
	if err := s.tmpl.ExecuteTemplate(w, "stats_cards", pageData{Stats: stats}); err != nil {
		s.log.Error("render stats_cards failed", "error", err)
	}
}

func (s *Server) handlePartialActive(w http.ResponseWriter, r *http.Request) {
	if err := s.tmpl.ExecuteTemplate(w, "active_migration", pageData{LiveMig: s.bot.ActiveMigration()}); err != nil {
		s.log.Error("render active_migration failed", "error", err)
	}
}

func (s *Server) handlePartialRecent(w http.ResponseWriter, r *http.Request) {
	recent, err := s.store.GetRecentMigrations(r.Context(), 5)
	if err != nil {
		s.log.Error("partial recent failed", "error", err)
	}
	if err := s.tmpl.ExecuteTemplate(w, "recent_migrations", pageData{Recent: recent}); err != nil {
		s.log.Error("render recent_migrations failed", "error", err)
	}
}

// --- Health Check ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"ok","uptime":"%s"}`, s.fmtUptime())
}

// --- Helpers ---

func (s *Server) render(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("render template failed", "template", name, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (s *Server) fmtUptime() string {
	d := s.bot.Uptime().Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 24 {
		return fmt.Sprintf("%dд %dч", h/24, h%24)
	}
	if h > 0 {
		return fmt.Sprintf("%dч %dм", h, m)
	}
	return fmt.Sprintf("%dм", m)
}
