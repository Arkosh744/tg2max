package admin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/arkosh/tg2max/internal/storage"
)

// --- Telegram WebApp Auth ---

// validateTelegramWebApp verifies the initData signature from Telegram WebApp.
// Returns the user's telegram ID if valid, 0 if invalid.
func (s *Server) validateTelegramWebApp(r *http.Request) int64 {
	initData := r.Header.Get("X-Telegram-Init-Data")
	if initData == "" {
		initData = r.URL.Query().Get("initData")
	}
	if initData == "" {
		return 0
	}

	// Parse initData as URL-encoded params
	params, err := url.ParseQuery(initData)
	if err != nil {
		return 0
	}

	// Extract hash
	hash := params.Get("hash")
	if hash == "" {
		return 0
	}

	// Build data-check-string: sorted key=value pairs (excluding hash), joined by \n
	params.Del("hash")
	var keys []string
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+params.Get(k))
	}
	dataCheckString := strings.Join(parts, "\n")

	// HMAC-SHA256 with secret = HMAC-SHA256("WebAppData", bot_token)
	secretKey := hmac.New(sha256.New, []byte("WebAppData"))
	secretKey.Write([]byte(s.cfg.BotToken))
	secret := secretKey.Sum(nil)

	h := hmac.New(sha256.New, secret)
	h.Write([]byte(dataCheckString))
	calculated := hex.EncodeToString(h.Sum(nil))

	if calculated != hash {
		return 0
	}

	// Extract user ID from the "user" JSON field
	userJSON := params.Get("user")
	if userJSON == "" {
		return 0
	}
	var user struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(userJSON), &user); err != nil {
		return 0
	}
	return user.ID
}

// requireWebAppAuth middleware for API endpoints — validates Telegram WebApp initData
// and checks that the user is an admin.
func (s *Server) requireWebAppAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := s.validateTelegramWebApp(r)
		if userID == 0 {
			s.jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Check admin list from config
		isAdmin := false
		for _, id := range s.cfg.AdminUserIDs {
			if id == userID {
				isAdmin = true
				break
			}
		}
		if !isAdmin {
			s.jsonError(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- JSON API Handlers ---

func (s *Server) handleAPIStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.GetStats(r.Context())
	if err != nil {
		s.jsonError(w, "failed to get stats", http.StatusInternalServerError)
		return
	}
	s.jsonOK(w, map[string]any{
		"uptime":           s.fmtUptime(),
		"total_users":      stats.TotalUsers,
		"total_migrations": stats.TotalMigrations,
		"completed":        stats.Completed,
		"failed":           stats.Failed,
		"cancelled":        stats.Cancelled,
		"total_sent":       stats.TotalSent,
		"avg_duration_sec": stats.AvgDurationSec,
	})
}

func (s *Server) handleAPILive(w http.ResponseWriter, r *http.Request) {
	live := s.bot.ActiveMigration()
	if live == nil {
		s.jsonOK(w, nil)
		return
	}
	s.jsonOK(w, map[string]any{
		"user_id":        live.UserID,
		"max_chat_name":  live.MaxChatName,
		"total_messages": live.TotalMessages,
		"sent_messages":  live.SentMessages,
		"percent":        live.Percent,
		"eta":            live.ETA,
		"elapsed":        live.Elapsed.String(),
		"speed":          live.Speed,
		"paused":         live.Paused,
	})
}

func (s *Server) handleAPIChart(w http.ResponseWriter, r *http.Request) {
	days := 30
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 && d <= 365 {
		days = d
	}
	stats, err := s.store.GetDailyStats(r.Context(), days)
	if err != nil {
		s.jsonError(w, "failed to get chart data", http.StatusInternalServerError)
		return
	}
	s.jsonOK(w, stats)
}

func (s *Server) handleAPIMigrations(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	perPage := 20
	if pp, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && pp > 0 && pp <= 100 {
		perPage = pp
	}
	status := r.URL.Query().Get("status")

	migrations, total, err := s.store.ListMigrations(r.Context(), storage.MigrationFilter{
		Status:  status,
		Page:    page,
		PerPage: perPage,
	})
	if err != nil {
		s.jsonError(w, "failed to list migrations", http.StatusInternalServerError)
		return
	}
	s.jsonOK(w, map[string]any{
		"migrations":  migrations,
		"total":       total,
		"page":        page,
		"total_pages": (total + perPage - 1) / perPage,
	})
}

func (s *Server) handleAPIMigrationDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}
	m, err := s.store.GetMigration(r.Context(), id)
	if err != nil {
		s.jsonError(w, "failed to get migration", http.StatusInternalServerError)
		return
	}
	if m == nil {
		s.jsonError(w, "not found", http.StatusNotFound)
		return
	}
	s.jsonOK(w, m)
}

func (s *Server) handleAPIUsers(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	users, total, err := s.store.ListUsers(r.Context(), page, 20)
	if err != nil {
		s.jsonError(w, "failed to list users", http.StatusInternalServerError)
		return
	}
	s.jsonOK(w, map[string]any{
		"users":       users,
		"total":       total,
		"page":        page,
		"total_pages": (total + 19) / 20,
	})
}

func (s *Server) handleAPIUserDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}
	u, err := s.store.GetUser(r.Context(), id)
	if err != nil || u == nil {
		s.jsonError(w, "not found", http.StatusNotFound)
		return
	}
	history, _ := s.store.GetUserHistory(r.Context(), id, 50)
	s.jsonOK(w, map[string]any{
		"user":    u,
		"history": history,
	})
}

// --- JSON Helpers ---

func (s *Server) jsonOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (s *Server) jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
