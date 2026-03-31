package admin

import (
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
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/" {
		http.NotFound(w, r)
		return
	}
	stats, _ := s.store.GetStats(r.Context())
	recent, _ := s.store.GetRecentMigrations(r.Context(), 5)

	s.render(w, "dashboard.html", pageData{
		Title:   "Dashboard",
		Active:  "dashboard",
		Uptime:  s.fmtUptime(),
		Stats:   stats,
		Recent:  recent,
		LiveMig: s.bot.ActiveMigration(),
	})
}

func (s *Server) handleMigrations(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	status := r.URL.Query().Get("status")

	migrations, total, _ := s.store.ListMigrations(r.Context(), storage.MigrationFilter{
		Status:  status,
		Page:    page,
		PerPage: 20,
	})

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
	if err != nil || m == nil {
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

	users, total, _ := s.store.ListUsers(r.Context(), page, 20)
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
	if err != nil || u == nil {
		http.NotFound(w, r)
		return
	}
	history, _ := s.store.GetUserHistory(r.Context(), id, 50)

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
	stats, _ := s.store.GetStats(r.Context())
	s.tmpl.ExecuteTemplate(w, "stats_cards", pageData{Stats: stats})
}

func (s *Server) handlePartialActive(w http.ResponseWriter, r *http.Request) {
	s.tmpl.ExecuteTemplate(w, "active_migration", pageData{LiveMig: s.bot.ActiveMigration()})
}

func (s *Server) handlePartialRecent(w http.ResponseWriter, r *http.Request) {
	recent, _ := s.store.GetRecentMigrations(r.Context(), 5)
	s.tmpl.ExecuteTemplate(w, "recent_migrations", pageData{Recent: recent})
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
