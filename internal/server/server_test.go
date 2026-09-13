package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spool-reader/spool/internal/auth"
)

const (
	testEmail = "user@example.com"
	testPass  = "password123"
)

func TestPageNumber(t *testing.T) {
	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/", want: 1},
		{path: "/?page=2", want: 2},
		{path: "/?page=0", want: 1},
		{path: "/?page=invalid", want: 1},
	} {
		req := httptest.NewRequest(http.MethodGet, test.path, nil)
		if got := pageNumber(req); got != test.want {
			t.Fatalf("pageNumber(%q) = %d, want %d", test.path, got, test.want)
		}
	}
}

func TestRichTextSanitizesContent(t *testing.T) {
	got := string(richText(`<p>Read <a href="https://example.com">more</a>.</p><script>alert(1)</script>`))
	if !strings.Contains(got, `<a href="https://example.com"`) {
		t.Fatalf("rich text = %q, want link", got)
	}
	if strings.Contains(got, "script") {
		t.Fatalf("rich text = %q, contains script", got)
	}
}

func TestStylesheet(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/assets/app.css", nil)
	rec := httptest.NewRecorder()

	NewMux(nil, nil, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/css; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
}

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	NewMux(slog.Default(), nil, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"status":"ok"}` {
		t.Fatalf("body = %q, want health response", got)
	}
}

func TestHome(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	NewMux(nil, nil, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "Spool\n" {
		t.Fatalf("body = %q, want Spool", got)
	}
}

func TestHomeRedirectsToLogin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	NewMux(nil, auth.NewService(newServerAuthStore()), nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/login" {
		t.Fatalf("location = %q, want /login", got)
	}
}

func TestFeedsRedirectsToLogin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/feeds", nil)
	rec := httptest.NewRecorder()

	NewMux(nil, auth.NewService(newServerAuthStore()), nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/login" {
		t.Fatalf("location = %q, want /login", got)
	}
}

func TestLoginRedirectsToSetup(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()

	NewMux(nil, auth.NewService(newServerAuthStore()), nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/setup" {
		t.Fatalf("location = %q, want /setup", got)
	}
}

func TestSetupCheckFailureReturnsServerError(t *testing.T) {
	store := newServerAuthStore()
	store.setupErr = errors.New("database unavailable")
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()

	NewMux(nil, auth.NewService(store), nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("location = %q, want empty", got)
	}
}

func TestSetupRejectsMissingCSRF(t *testing.T) {
	req := formRequest(http.MethodPost, "/setup", url.Values{
		"email":    {testEmail},
		"password": {testPass},
	})
	rec := httptest.NewRecorder()

	NewMux(nil, auth.NewService(newServerAuthStore()), nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestSetupCreatesSession(t *testing.T) {
	store := newServerAuthStore()
	req := csrfFormRequest(http.MethodPost, "/setup", url.Values{
		"email":    {testEmail},
		"password": {testPass},
	})
	rec := httptest.NewRecorder()

	NewMux(nil, auth.NewService(store), nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Fatal("session cookie was not set")
	}
}

func TestLoginRejectsInvalidCredentials(t *testing.T) {
	store := newServerAuthStore()
	svc := auth.NewService(store)
	if _, err := svc.Setup(context.Background(), testEmail, testPass); err != nil {
		t.Fatalf("Setup returned error: %v", err)
	}

	req := csrfFormRequest(http.MethodPost, "/login", url.Values{
		"email":    {testEmail},
		"password": {"wrong-password"},
	})
	rec := httptest.NewRecorder()
	NewMux(nil, svc, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuthenticatedHome(t *testing.T) {
	store := newServerAuthStore()
	svc := auth.NewService(store)
	result, err := svc.Setup(context.Background(), testEmail, testPass)
	if err != nil {
		t.Fatalf("Setup returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: result.Token})
	rec := httptest.NewRecorder()
	NewMux(nil, svc, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), testEmail) {
		t.Fatalf("body = %q, want email", rec.Body.String())
	}
}

func TestLoginAndLogout(t *testing.T) {
	store := newServerAuthStore()
	svc := auth.NewService(store)
	if _, err := svc.Setup(context.Background(), testEmail, testPass); err != nil {
		t.Fatalf("Setup returned error: %v", err)
	}

	loginReq := csrfFormRequest(http.MethodPost, "/login", url.Values{
		"email":    {testEmail},
		"password": {testPass},
	})
	loginRec := httptest.NewRecorder()
	NewMux(nil, svc, nil).ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusSeeOther)
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("session cookie was not set")
	}

	logoutReq := csrfFormRequest(http.MethodPost, "/logout", nil)
	logoutReq.AddCookie(cookies[0])
	logoutRec := httptest.NewRecorder()
	NewMux(nil, svc, nil).ServeHTTP(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusSeeOther {
		t.Fatalf("logout status = %d, want %d", logoutRec.Code, http.StatusSeeOther)
	}
	if got := logoutRec.Header().Get("Location"); got != "/login" {
		t.Fatalf("location = %q, want /login", got)
	}

	homeReq := httptest.NewRequest(http.MethodGet, "/", nil)
	homeReq.AddCookie(cookies[0])
	homeRec := httptest.NewRecorder()
	NewMux(nil, svc, nil).ServeHTTP(homeRec, homeReq)
	if homeRec.Code != http.StatusSeeOther {
		t.Fatalf("home status = %d, want %d", homeRec.Code, http.StatusSeeOther)
	}
}

func formRequest(method, path string, form url.Values) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func csrfFormRequest(method, path string, form url.Values) *http.Request {
	const token = "csrf-token"

	if form == nil {
		form = make(url.Values)
	}
	form.Set(csrfFormField, token)
	req := formRequest(method, path, form)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	return req
}

type serverAuthStore struct {
	mu           sync.Mutex
	setupErr     error
	usersByEmail map[string]auth.User
	sessions     map[string]auth.Session
	nextID       int
}

func newServerAuthStore() *serverAuthStore {
	return &serverAuthStore{
		usersByEmail: make(map[string]auth.User),
		sessions:     make(map[string]auth.Session),
	}
}

func (s *serverAuthStore) SetupRequired(ctx context.Context) (bool, error) {
	if s.setupErr != nil {
		return false, s.setupErr
	}
	return len(s.usersByEmail) == 0, nil
}

func (s *serverAuthStore) CreateFirstUser(ctx context.Context, email, passwordHash, role string) (auth.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.usersByEmail) != 0 {
		return auth.User{}, auth.ErrSetupComplete
	}
	return s.createUser(email, passwordHash, role)
}

func (s *serverAuthStore) createUser(email, passwordHash, role string) (auth.User, error) {
	now := time.Now().UTC()
	user := auth.User{ID: s.id(), Email: email, PasswordHash: passwordHash, Role: role, CreatedAt: now, UpdatedAt: now}
	s.usersByEmail[email] = user
	return user, nil
}

func (s *serverAuthStore) FindUserByEmail(ctx context.Context, email string) (auth.User, error) {
	user, ok := s.usersByEmail[email]
	if !ok {
		return auth.User{}, auth.ErrInvalidCredentials
	}
	return user, nil
}

func (s *serverAuthStore) FindUserBySessionTokenHash(ctx context.Context, tokenHash string, now time.Time) (auth.User, auth.Session, error) {
	for _, session := range s.sessions {
		if session.TokenHash != tokenHash || !session.ExpiresAt.After(now) {
			continue
		}
		for _, user := range s.usersByEmail {
			if user.ID == session.UserID {
				return user, session, nil
			}
		}
	}
	return auth.User{}, auth.Session{}, auth.ErrSessionNotFound
}

func (s *serverAuthStore) CreateSession(ctx context.Context, userID, tokenHash string, expiresAt time.Time) (auth.Session, error) {
	session := auth.Session{ID: s.id(), UserID: userID, TokenHash: tokenHash, ExpiresAt: expiresAt, CreatedAt: time.Now().UTC()}
	s.sessions[session.ID] = session
	return session, nil
}

func (s *serverAuthStore) DeleteSessionByTokenHash(ctx context.Context, tokenHash string) error {
	for id, session := range s.sessions {
		if session.TokenHash == tokenHash {
			delete(s.sessions, id)
		}
	}
	return nil
}

func (s *serverAuthStore) TouchSession(ctx context.Context, id string, seenAt time.Time) error {
	session := s.sessions[id]
	session.LastSeenAt = &seenAt
	s.sessions[id] = session
	return nil
}

func (s *serverAuthStore) id() string {
	s.nextID++
	return "id-" + strconv.Itoa(s.nextID)
}
