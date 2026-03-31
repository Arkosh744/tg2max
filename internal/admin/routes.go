package admin

import "net/http"

func (s *Server) setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("GET /admin/login", s.handleLoginPage)
	mux.HandleFunc("POST /admin/login", s.handleLoginSubmit)
	mux.HandleFunc("POST /admin/logout", s.handleLogout)

	// Protected pages
	mux.Handle("GET /admin/", s.requireAuth(http.HandlerFunc(s.handleDashboard)))
	mux.Handle("GET /admin/migrations", s.requireAuth(http.HandlerFunc(s.handleMigrations)))
	mux.Handle("GET /admin/migrations/{id}", s.requireAuth(http.HandlerFunc(s.handleMigrationDetail)))
	mux.Handle("GET /admin/users", s.requireAuth(http.HandlerFunc(s.handleUsers)))
	mux.Handle("GET /admin/users/{id}", s.requireAuth(http.HandlerFunc(s.handleUserDetail)))

	// HTMX partials (polling fallback)
	mux.Handle("GET /admin/partials/stats", s.requireAuth(http.HandlerFunc(s.handlePartialStats)))
	mux.Handle("GET /admin/partials/active", s.requireAuth(http.HandlerFunc(s.handlePartialActive)))
	mux.Handle("GET /admin/partials/recent", s.requireAuth(http.HandlerFunc(s.handlePartialRecent)))

	// SSE live updates
	mux.Handle("GET /admin/events", s.requireAuth(http.HandlerFunc(s.handleSSE)))

	return mux
}
