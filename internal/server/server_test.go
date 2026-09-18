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
	"github.com/spool-reader/spool/internal/core"
)

const (
	testEmail = "user@example.com"
	testPass  = "password123"
)

func TestDisplayIconURL(t *testing.T) {
	feed := core.Feed{SiteURL: "https://lobste.rs/"}
	if got := displayIconURL(feed); got != "https://lobste.rs/favicon.ico" {
		t.Fatalf("displayIconURL = %q", got)
	}
}

func TestFormatDate(t *testing.T) {
	date := time.Date(2026, time.September, 13, 0, 0, 0, 0, time.UTC)
	if got := formatDate(&date); got != "Sep 13, 2026" {
		t.Fatalf("formatDate = %q", got)
	}
	if got := formatDate(nil); got != "" {
		t.Fatalf("formatDate(nil) = %q", got)
	}
}

func TestRedirectTargetUsesReturnTo(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/items/item-1/read", strings.NewReader("return_to=%2Ffeeds%2Ffeed-1%3Fpage%3D2"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatal(err)
	}

	if got := redirectTarget(req); got != "/feeds/feed-1?page=2" {
		t.Fatalf("redirectTarget = %q", got)
	}
}

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

func TestRichTextSanitizesActiveContent(t *testing.T) {
	for _, payload := range []string{
		`<img src="x" onerror="alert(1)">`,
		`<a href="javascript:alert(1)">click</a>`,
		`<svg onload="alert(1)"><circle></circle></svg>`,
		`<iframe src="https://example.com"></iframe>`,
	} {
		got := strings.ToLower(string(richText(payload)))
		for _, blocked := range []string{"javascript:", "onerror", "onload", "<svg", "<iframe"} {
			if strings.Contains(got, blocked) {
				t.Fatalf("richText(%q) = %q, contains %q", payload, got, blocked)
			}
		}
	}
}

func TestHomeTemplateEscapesFeedContent(t *testing.T) {
	data := pageData{
		CSRFToken: "csrf-token",
		Email:     `<script>alert(1)</script>@example.com`,
		Feeds: []core.Feed{{
			ID:      "feed-1",
			Title:   `<script>alert(1)</script>`,
			IconURL: `javascript:alert(1)`,
		}},
		FeedNames: map[string]string{"feed-1": `<script>alert(1)</script>`},
		FeedIcons: map[string]string{"feed-1": `javascript:alert(1)`},
		Items: []core.Item{{
			ID:      "item-1",
			FeedID:  "feed-1",
			URL:     `javascript:alert(1)`,
			Title:   `<script>alert(1)</script>`,
			Summary: `<img src="x" onerror="alert(1)"><a href="javascript:alert(2)">bad</a><script>alert(3)</script>`,
		}},
		Page: firstPage,
	}
	rec := httptest.NewRecorder()

	render(rec, "home.html", data)

	body := strings.ToLower(rec.Body.String())
	for _, blocked := range []string{"<script>alert", "javascript:alert", "onerror"} {
		if strings.Contains(body, blocked) {
			t.Fatalf("rendered home contains %q: %s", blocked, rec.Body.String())
		}
	}
	if !strings.Contains(rec.Body.String(), "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("rendered home does not escape script text: %s", rec.Body.String())
	}
}

func TestTemplatesExposePluginSlots(t *testing.T) {
	for _, path := range []string{
		"templates/home.html",
		"templates/feeds.html",
		"templates/feed.html",
	} {
		content, err := templateFiles.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q) returned error: %v", path, err)
		}
		if !strings.Contains(string(content), `data-plugin-slot=`) {
			t.Fatalf("template %q has no plugin slot", path)
		}
	}
}

func TestReadingTemplatesExposeFeedSidebar(t *testing.T) {
	for _, path := range []string{"templates/home.html", "templates/feed.html"} {
		content, err := templateFiles.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q) returned error: %v", path, err)
		}
		if !strings.Contains(string(content), `class="feed-sidebar"`) {
			t.Fatalf("template %q has no feed sidebar", path)
		}
	}
}

func TestFeedTemplatesExposeUnreadCounts(t *testing.T) {
	for _, path := range []string{"templates/home.html", "templates/feed.html", "templates/feeds.html"} {
		content, err := templateFiles.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q) returned error: %v", path, err)
		}
		if !strings.Contains(string(content), `class="unread-count"`) {
			t.Fatalf("template %q has no unread count", path)
		}
	}
}

func TestReadingTemplatesExposeMarkAllRead(t *testing.T) {
	for _, test := range []struct {
		path string
		want string
	}{
		{path: "templates/home.html", want: `action="/items/read"`},
		{path: "templates/feed.html", want: `action="/feeds/{{.Feed.ID}}/read"`},
	} {
		content, err := templateFiles.ReadFile(test.path)
		if err != nil {
			t.Fatalf("ReadFile(%q) returned error: %v", test.path, err)
		}
		if !strings.Contains(string(content), test.want) {
			t.Fatalf("template %q missing %q", test.path, test.want)
		}
	}
}

func TestReadingTemplatesMarkTitleLinksRead(t *testing.T) {
	for _, path := range []string{"templates/home.html", "templates/feed.html"} {
		content, err := templateFiles.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q) returned error: %v", path, err)
		}
		if !strings.Contains(string(content), `data-read-url="/items/{{.ID}}/read"`) {
			t.Fatalf("template %q title links do not mark read", path)
		}
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

func TestScript(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	rec := httptest.NewRecorder()

	NewMux(nil, nil, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
}

func TestStylesheetConstrainsEmbeddedImages(t *testing.T) {
	stylesheet := string(appCSS)

	for _, want := range []string{".item-summary img", ".feed-description img", "max-width: 100%", "height: auto"} {
		if !strings.Contains(stylesheet, want) {
			t.Fatalf("stylesheet missing %q", want)
		}
	}
}

func TestStylesheetHidesSidebarOnMobile(t *testing.T) {
	stylesheet := string(appCSS)

	for _, want := range []string{".feed-sidebar", "@media (max-width: 640px)", "display: none"} {
		if !strings.Contains(stylesheet, want) {
			t.Fatalf("stylesheet missing %q", want)
		}
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
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("Content-Security-Policy is empty")
	}
}

func TestReadyz(t *testing.T) {
	handler := newMux(nil, nil, nil, false, func(context.Context) error {
		return errors.New("database unavailable")
	}, nil, "")
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestMetrics(t *testing.T) {
	handler := newMux(nil, nil, nil, false, nil, func() string {
		return "spool_refresh_succeeded_total 1\n"
	}, "")
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rec.Body.String(); got != "spool_refresh_succeeded_total 1\n" {
		t.Fatalf("body = %q", got)
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
	newMux(nil, svc, nil, true, nil, nil, "").ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusSeeOther)
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("session cookie was not set")
	}
	if !cookies[0].Secure {
		t.Fatal("session cookie is not secure")
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
