package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSignVerifyToken(t *testing.T) {
	s := &Server{secret: []byte("test-secret-key")}

	expiry := time.Now().Add(1 * time.Hour)
	token := s.signToken(expiry)

	if token == "" {
		t.Fatal("signToken returned empty string")
	}
	if !s.verifyToken(token) {
		t.Error("verifyToken returned false for valid token")
	}
}

func TestVerifyToken_Expired(t *testing.T) {
	s := &Server{secret: []byte("test-secret-key")}

	expiry := time.Now().Add(-1 * time.Hour)
	token := s.signToken(expiry)

	if s.verifyToken(token) {
		t.Error("verifyToken returned true for expired token")
	}
}

func TestVerifyToken_Tampered(t *testing.T) {
	s := &Server{secret: []byte("test-secret-key")}

	expiry := time.Now().Add(1 * time.Hour)
	token := s.signToken(expiry)

	// Tamper with signature
	parts := strings.SplitN(token, ".", 2)
	tampered := parts[0] + ".deadbeef"

	if s.verifyToken(tampered) {
		t.Error("verifyToken returned true for tampered token")
	}
}

func TestVerifyToken_WrongSecret(t *testing.T) {
	s1 := &Server{secret: []byte("secret-1")}
	s2 := &Server{secret: []byte("secret-2")}

	token := s1.signToken(time.Now().Add(1 * time.Hour))

	if s2.verifyToken(token) {
		t.Error("verifyToken accepted token signed with different secret")
	}
}

func TestVerifyToken_InvalidFormat(t *testing.T) {
	s := &Server{secret: []byte("test")}

	cases := []string{"", "no-dot", "not-a-number.sig", ".."}
	for _, tc := range cases {
		if s.verifyToken(tc) {
			t.Errorf("verifyToken accepted invalid token %q", tc)
		}
	}
}

func TestRequireAuth_Redirect(t *testing.T) {
	s := &Server{secret: []byte("test"), cfg: Config{Password: "pass"}}

	handler := s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/admin/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/login" {
		t.Errorf("expected redirect to /admin/login, got %s", loc)
	}
}

func TestRequireAuth_ValidCookie(t *testing.T) {
	s := &Server{secret: []byte("test"), cfg: Config{Password: "pass"}}
	token := s.signToken(time.Now().Add(1 * time.Hour))

	handler := s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/admin/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with valid cookie, got %d", rec.Code)
	}
}

func TestRequireAuth_HTMX401(t *testing.T) {
	s := &Server{secret: []byte("test"), cfg: Config{Password: "pass"}}

	handler := s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/admin/partials/stats", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for HTMX request without auth, got %d", rec.Code)
	}
}
