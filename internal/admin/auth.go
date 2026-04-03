package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	cookieName     = "tg2max_admin"
	csrfCookieName = "tg2max_csrf"
	sessionTTL     = 24 * time.Hour
	maxLoginAttempts = 5
	loginBlockDuration = 15 * time.Minute
)

// loginAttempt tracks failed login attempts per IP.
type loginAttempt struct {
	count     int
	firstAt   time.Time
	blockedAt time.Time
}

var (
	loginAttempts sync.Map // IP -> *loginAttempt
)

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	csrf := s.generateCSRFToken(w)
	s.tmpl.ExecuteTemplate(w, "login.html", map[string]any{"CSRFToken": csrf})
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	// Rate limiting by IP
	ip := extractIP(r)
	if blocked := s.checkLoginRateLimit(ip); blocked {
		http.Error(w, "Слишком много попыток. Попробуйте через 15 минут.", http.StatusTooManyRequests)
		return
	}

	// CSRF check
	if !s.validateCSRF(r) {
		http.Error(w, "Недействительный CSRF токен", http.StatusForbidden)
		return
	}

	password := r.FormValue("password")
	if subtle.ConstantTimeCompare([]byte(password), []byte(s.cfg.Password)) != 1 {
		s.recordFailedLogin(ip)
		csrf := s.generateCSRFToken(w)
		s.tmpl.ExecuteTemplate(w, "login.html", map[string]any{"Error": "Неверный пароль", "CSRFToken": csrf})
		return
	}

	// Successful login — reset attempts
	loginAttempts.Delete(ip)

	secure := os.Getenv("ADMIN_SECURE_COOKIE") != "false"
	expiry := time.Now().Add(sessionTTL)
	token := s.signToken(expiry)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiry,
	})
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

// checkLoginRateLimit returns true if the IP is currently blocked.
func (s *Server) checkLoginRateLimit(ip string) bool {
	val, ok := loginAttempts.Load(ip)
	if !ok {
		return false
	}
	attempt := val.(*loginAttempt)
	if !attempt.blockedAt.IsZero() && time.Since(attempt.blockedAt) < loginBlockDuration {
		return true
	}
	// Reset if block expired
	if !attempt.blockedAt.IsZero() && time.Since(attempt.blockedAt) >= loginBlockDuration {
		loginAttempts.Delete(ip)
		return false
	}
	return false
}

func (s *Server) recordFailedLogin(ip string) {
	val, _ := loginAttempts.LoadOrStore(ip, &loginAttempt{firstAt: time.Now()})
	attempt := val.(*loginAttempt)
	attempt.count++
	if attempt.count >= maxLoginAttempts {
		attempt.blockedAt = time.Now()
		s.log.Warn("admin login blocked", "ip", ip, "attempts", attempt.count)
	}
}

// generateCSRFToken creates a random token, sets it as a cookie, and returns it for the form.
func (s *Server) generateCSRFToken(w http.ResponseWriter) string {
	buf := make([]byte, 32)
	rand.Read(buf)
	token := hex.EncodeToString(buf)
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/admin",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   3600,
	})
	return token
}

// validateCSRF checks that the form token matches the cookie token.
func (s *Server) validateCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	formToken := r.FormValue("csrf_token")
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(formToken)) == 1
}

func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.Split(xff, ",")[0]
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	return strings.Split(r.RemoteAddr, ":")[0]
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/admin",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil || !s.verifyToken(cookie.Value) {
			if strings.Contains(r.Header.Get("HX-Request"), "true") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// signToken creates an HMAC-signed token with expiry timestamp.
func (s *Server) signToken(expiry time.Time) string {
	payload := strconv.FormatInt(expiry.Unix(), 10)
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%s.%s", payload, sig)
}

// verifyToken checks the HMAC signature and expiry.
func (s *Server) verifyToken(token string) bool {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > expiry {
		return false
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(parts[0]))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(parts[1]), []byte(expected))
}
