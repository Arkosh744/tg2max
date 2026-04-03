package admin

import "net/http"

func (s *Server) setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	// Health check (no auth — used by Docker healthcheck / load balancers)
	mux.HandleFunc("GET /health", s.handleHealth)

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

	// JSON API for Telegram Mini App (WebApp auth)
	mux.Handle("GET /api/stats", s.requireWebAppAuth(http.HandlerFunc(s.handleAPIStats)))
	mux.Handle("GET /api/live", s.requireWebAppAuth(http.HandlerFunc(s.handleAPILive)))
	mux.Handle("GET /api/chart", s.requireWebAppAuth(http.HandlerFunc(s.handleAPIChart)))
	mux.Handle("GET /api/migrations", s.requireWebAppAuth(http.HandlerFunc(s.handleAPIMigrations)))
	mux.Handle("GET /api/migrations/{id}", s.requireWebAppAuth(http.HandlerFunc(s.handleAPIMigrationDetail)))
	mux.Handle("GET /api/users", s.requireWebAppAuth(http.HandlerFunc(s.handleAPIUsers)))
	mux.Handle("GET /api/users/{id}", s.requireWebAppAuth(http.HandlerFunc(s.handleAPIUserDetail)))

	// Telegram Mini App static page
	mux.HandleFunc("GET /miniapp", s.handleMiniApp)

	return mux
}
