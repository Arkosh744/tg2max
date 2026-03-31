package admin

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/arkosh/tg2max/internal/storage"
	"github.com/arkosh/tg2max/pkg/models"
)

//go:embed templates/*.html templates/partials/*.html
var templateFS embed.FS

// BotInfo provides read-only access to live bot state.
type BotInfo interface {
	Uptime() time.Duration
	ActiveMigration() *models.LiveMigration
}

// Config holds admin server configuration.
type Config struct {
	Addr     string // listen address, e.g. ":8080"
	Password string // admin password
	Secret   string // cookie signing secret
}

// Server is the admin web UI HTTP server.
type Server struct {
	store   storage.Storage
	bot     BotInfo
	log     *slog.Logger
	cfg     Config
	secret  []byte
	tmpl    *template.Template
	mux     *http.ServeMux
}

// New creates a new admin server.
func New(store storage.Storage, bot BotInfo, cfg Config, log *slog.Logger) *Server {
	s := &Server{
		store:  store,
		bot:    bot,
		log:    log,
		cfg:    cfg,
		secret: []byte(cfg.Secret),
	}
	s.tmpl = s.parseTemplates()
	s.mux = s.setupRoutes()
	return s
}

// ListenAndServe starts the HTTP server with graceful shutdown on ctx cancellation.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:         s.cfg.Addr,
		Handler:      s.mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()
	s.log.Info("admin server listening", "addr", s.cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("admin server: %w", err)
	}
	return nil
}

func (s *Server) parseTemplates() *template.Template {
	funcMap := template.FuncMap{
		"formatTime": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.Format("02.01.2006 15:04")
		},
		"formatDuration": func(sec int) string {
			d := time.Duration(sec) * time.Second
			if d < time.Minute {
				return fmt.Sprintf("%dс", sec)
			}
			h := int(d.Hours())
			m := int(d.Minutes()) % 60
			if h > 0 {
				return fmt.Sprintf("%dч %dм", h, m)
			}
			return fmt.Sprintf("%dм", m)
		},
		"formatUptime": func(d time.Duration) string {
			d = d.Round(time.Second)
			h := int(d.Hours())
			m := int(d.Minutes()) % 60
			if h > 24 {
				days := h / 24
				h = h % 24
				return fmt.Sprintf("%dд %dч %dм", days, h, m)
			}
			if h > 0 {
				return fmt.Sprintf("%dч %dм", h, m)
			}
			return fmt.Sprintf("%dм", m)
		},
		"statusBadge": func(status string) string {
			switch status {
			case "completed":
				return "bg-green-500/20 text-green-400"
			case "failed":
				return "bg-red-500/20 text-red-400"
			case "cancelled":
				return "bg-yellow-500/20 text-yellow-400"
			case "started":
				return "bg-blue-500/20 text-blue-400"
			default:
				return "bg-slate-500/20 text-slate-400"
			}
		},
		"statusLabel": func(status string) string {
			switch status {
			case "completed":
				return "Завершена"
			case "failed":
				return "Ошибка"
			case "cancelled":
				return "Отменена"
			case "started":
				return "В процессе"
			default:
				return status
			}
		},
		"sub": func(a, b int) int { return a - b },
		"add": func(a, b int) int { return a + b },
		"seq": func(from, to int) []int {
			var s []int
			for i := from; i <= to; i++ {
				s = append(s, i)
			}
			return s
		},
	}
	return template.Must(
		template.New("").Funcs(funcMap).ParseFS(templateFS,
			"templates/*.html",
			"templates/partials/*.html",
		),
	)
}
