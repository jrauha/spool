package server

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/spool-reader/spool/internal/auth"
	"github.com/spool-reader/spool/internal/config"
	"github.com/spool-reader/spool/internal/core"
	"github.com/spool-reader/spool/internal/feed"
)

const (
	sessionCookiePath   = "/"
	csrfCookieName      = "spool_csrf"
	csrfFormField       = "csrf_token"
	maxFormBytes        = 1 << 20
	serverReadTimeout   = 15 * time.Second
	serverWriteTimeout  = 30 * time.Second
	serverIdleTimeout   = 60 * time.Second
	serverHeaderTimeout = 5 * time.Second
)

//go:embed templates/*.html
var templateFiles embed.FS

//go:embed assets/app.css
var appCSS []byte

//go:embed assets/app.js
var appJS []byte

var richTextPolicy = bluemonday.UGCPolicy()

var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"formatDate": formatDate,
	"richText":   richText,
}).ParseFS(templateFiles, "templates/*.html"))

type contextKey string

const userContextKey contextKey = "user"

const (
	itemsPerPage = 20
	firstPage    = 1
)

type App struct {
	log          *slog.Logger
	auth         *auth.Service
	feeds        *feed.Service
	cookieSecure bool
	ready        func(context.Context) error
	metrics      func() string
	setupToken   string
}

type pageData struct {
	CSRFToken    string
	Email        string
	Notice       string
	Feed         *core.Feed
	Feeds        []core.Feed
	FeedNames    map[string]string
	FeedIcons    map[string]string
	Items        []core.Item
	Page         int
	PreviousPage int
	NextPage     int
	HasNextPage  bool
	ReturnTo     string
	ResetEnabled bool
	ResetToken   string
}

func New(cfg config.Config, log *slog.Logger, authSvc *auth.Service, feedSvc *feed.Service, ready func(context.Context) error, metrics func() string) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           newMux(log, authSvc, feedSvc, cfg.CookieSecure, ready, metrics, cfg.SetupToken),
		ReadTimeout:       serverReadTimeout,
		ReadHeaderTimeout: serverHeaderTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
	}
}

func NewMux(log *slog.Logger, authSvc *auth.Service, feedSvc *feed.Service) http.Handler {
	return newMux(log, authSvc, feedSvc, false, nil, nil, "")
}

func newMux(log *slog.Logger, authSvc *auth.Service, feedSvc *feed.Service, cookieSecure bool, ready func(context.Context) error, metrics func() string, setupToken string) http.Handler {
	if log == nil {
		log = slog.Default()
	}

	app := &App{log: log, auth: authSvc, feeds: feedSvc, cookieSecure: cookieSecure, ready: ready, metrics: metrics, setupToken: setupToken}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /readyz", app.readyz)
	mux.HandleFunc("GET /metrics", app.metricsHandler)
	mux.HandleFunc("GET /assets/app.css", stylesheet)
	mux.HandleFunc("GET /assets/app.js", script)
	mux.HandleFunc("GET /setup", app.setupForm)
	mux.HandleFunc("POST /setup", app.setup)
	mux.HandleFunc("GET /login", app.loginForm)
	mux.HandleFunc("POST /login", app.login)
	mux.HandleFunc("GET /forgot-password", app.forgotPasswordForm)
	mux.HandleFunc("POST /forgot-password", app.requestPasswordReset)
	mux.HandleFunc("GET /reset", app.resetPasswordForm)
	mux.HandleFunc("POST /reset", app.resetPassword)
	mux.HandleFunc("POST /logout", app.logout)
	mux.Handle("GET /", app.requireAuth(http.HandlerFunc(app.home)))
	mux.Handle("GET /feeds", app.requireAuth(http.HandlerFunc(app.feedList)))
	mux.Handle("GET /feeds/{id}", app.requireAuth(http.HandlerFunc(app.feedDetail)))
	mux.Handle("POST /feeds", app.requireAuth(http.HandlerFunc(app.addFeed)))
	mux.Handle("POST /feeds/{id}/refresh", app.requireAuth(http.HandlerFunc(app.refreshFeed)))
	mux.Handle("POST /feeds/{id}/delete", app.requireAuth(http.HandlerFunc(app.deleteFeed)))
	mux.Handle("POST /feeds/{id}/read", app.requireAuth(http.HandlerFunc(app.markFeedRead)))
	mux.Handle("POST /items/read", app.requireAuth(http.HandlerFunc(app.markAllRead)))
	mux.Handle("POST /items/{id}/read", app.requireAuth(http.HandlerFunc(app.markItemRead)))
	mux.Handle("POST /items/{id}/unread", app.requireAuth(http.HandlerFunc(app.markItemUnread)))

	return securityHeaders(requestLogger(log, mux))
}

func formatDate(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format("Jan 2, 2006")
}

func richText(value string) template.HTML {
	return template.HTML(richTextPolicy.Sanitize(value))
}

func stylesheet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(appCSS)
}

func script(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(appJS)
}

func pageNumber(r *http.Request) int {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < firstPage {
		return firstPage
	}
	return page
}

func pageNotice(r *http.Request) string {
	switch r.URL.Query().Get("notice") {
	case "added":
		return "Feed added. Its first refresh is queued."
	case "queued":
		return "Refresh queued."
	default:
		return ""
	}
}

func redirectTarget(r *http.Request) string {
	if target := localRedirectPath(r.FormValue("return_to")); target != "" {
		return target
	}

	referrer := r.Header.Get("Referer")
	parsedURL, err := url.Parse(referrer)
	if err == nil && parsedURL.IsAbs() && parsedURL.Host == r.Host {
		return parsedURL.RequestURI()
	}
	if target := localRedirectPath(referrer); target != "" {
		return target
	}
	return "/"
}

func localRedirectPath(value string) string {
	parsedURL, err := url.Parse(value)
	if err != nil || parsedURL.IsAbs() {
		return ""
	}
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		return value
	}
	return ""
}

func feedNames(feeds []core.Feed) map[string]string {
	names := make(map[string]string, len(feeds))
	for _, feed := range feeds {
		names[feed.ID] = feed.Title
	}
	return names
}

func displayIconURL(feed core.Feed) string {
	if feed.IconURL != "" {
		return feed.IconURL
	}
	parsedURL, err := url.ParseRequestURI(feed.SiteURL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return ""
	}
	parsedURL.Path = "/favicon.ico"
	parsedURL.RawQuery = ""
	parsedURL.Fragment = ""
	return parsedURL.String()
}

func withDisplayIcons(feeds []core.Feed) []core.Feed {
	for index := range feeds {
		feeds[index].IconURL = displayIconURL(feeds[index])
	}
	return feeds
}

func feedIcons(feeds []core.Feed) map[string]string {
	icons := make(map[string]string, len(feeds))
	for _, feed := range feeds {
		if feed.IconURL != "" {
			icons[feed.ID] = feed.IconURL
		}
	}
	return icons
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (a *App) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if a.metrics == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(a.metrics()))
}

func (a *App) readyz(w http.ResponseWriter, r *http.Request) {
	if a.ready != nil && a.ready(r.Context()) != nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	healthz(w, r)
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userContextKey).(auth.User)
	if a.auth == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Spool\n"))
		return
	}
	page := pageNumber(r)
	data := pageData{Email: user.Email, Notice: pageNotice(r), Page: page}
	if a.feeds != nil {
		var err error
		data.Feeds, err = a.feeds.ListFeedsForUser(r.Context(), user.ID)
		if err != nil {
			http.Error(w, "feeds unavailable", http.StatusInternalServerError)
			return
		}
		data.Feeds = withDisplayIcons(data.Feeds)
		data.Items, err = a.feeds.LatestForUser(r.Context(), user.ID, itemsPerPage+1, (page-firstPage)*itemsPerPage)
		if err != nil {
			http.Error(w, "items unavailable", http.StatusInternalServerError)
			return
		}
		if len(data.Items) > itemsPerPage {
			data.Items = data.Items[:itemsPerPage]
			data.HasNextPage = true
			data.NextPage = page + 1
		}
		if page > firstPage {
			data.PreviousPage = page - 1
		}
	}
	data.FeedNames = feedNames(data.Feeds)
	data.FeedIcons = feedIcons(data.Feeds)
	a.renderPage(w, r, "home.html", data)
}

func (a *App) feedList(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userContextKey).(auth.User)
	data := pageData{Email: user.Email, Notice: pageNotice(r)}
	if a.feeds != nil {
		var err error
		data.Feeds, err = a.feeds.ListFeedsForUser(r.Context(), user.ID)
		if err != nil {
			http.Error(w, "feeds unavailable", http.StatusInternalServerError)
			return
		}
		data.Feeds = withDisplayIcons(data.Feeds)
	}
	a.renderPage(w, r, "feeds.html", data)
}

func (a *App) feedDetail(w http.ResponseWriter, r *http.Request) {
	if a.feeds == nil {
		http.NotFound(w, r)
		return
	}
	user, _ := r.Context().Value(userContextKey).(auth.User)
	feed, err := a.feeds.FindForUser(r.Context(), user.ID, r.PathValue("id"))
	if errors.Is(err, core.ErrFeedNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "feed unavailable", http.StatusInternalServerError)
		return
	}
	page := pageNumber(r)
	feed.IconURL = displayIconURL(feed)
	feeds, err := a.feeds.ListFeedsForUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "feeds unavailable", http.StatusInternalServerError)
		return
	}
	items, err := a.feeds.ItemsForUser(r.Context(), user.ID, feed.ID, itemsPerPage+1, (page-firstPage)*itemsPerPage)
	if err != nil {
		http.Error(w, "items unavailable", http.StatusInternalServerError)
		return
	}
	data := pageData{Email: user.Email, Notice: pageNotice(r), Feed: &feed, Feeds: withDisplayIcons(feeds), Items: items, Page: page}
	if len(data.Items) > itemsPerPage {
		data.Items = data.Items[:itemsPerPage]
		data.HasNextPage = true
		data.NextPage = page + 1
	}
	if page > firstPage {
		data.PreviousPage = page - 1
	}
	a.renderPage(w, r, "feed.html", data)
}

func (a *App) addFeed(w http.ResponseWriter, r *http.Request) {
	if a.feeds == nil {
		http.NotFound(w, r)
		return
	}
	if !a.parseCSRFForm(w, r) {
		return
	}
	user, _ := r.Context().Value(userContextKey).(auth.User)
	if _, err := a.feeds.Add(r.Context(), user.ID, r.FormValue("url")); err != nil {
		http.Error(w, "feed add failed", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/feeds?notice=added", http.StatusSeeOther)
}

func (a *App) deleteFeed(w http.ResponseWriter, r *http.Request) {
	if a.feeds == nil {
		http.NotFound(w, r)
		return
	}
	if !a.parseCSRFForm(w, r) {
		return
	}
	user, _ := r.Context().Value(userContextKey).(auth.User)
	if err := a.feeds.Delete(r.Context(), user.ID, r.PathValue("id")); err != nil {
		if errors.Is(err, core.ErrFeedNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "feed delete failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/feeds", http.StatusSeeOther)
}

func (a *App) refreshFeed(w http.ResponseWriter, r *http.Request) {
	if a.feeds == nil {
		http.NotFound(w, r)
		return
	}
	if !a.parseCSRFForm(w, r) {
		return
	}
	user, _ := r.Context().Value(userContextKey).(auth.User)
	if _, err := a.feeds.FindForUser(r.Context(), user.ID, r.PathValue("id")); err != nil {
		if errors.Is(err, core.ErrFeedNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "feed refresh failed", http.StatusInternalServerError)
		return
	}
	if err := a.feeds.QueueRefresh(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, core.ErrFeedNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "feed refresh failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/feeds/"+r.PathValue("id")+"?notice=queued", http.StatusSeeOther)
}

func (a *App) markFeedRead(w http.ResponseWriter, r *http.Request) {
	if a.feeds == nil {
		http.NotFound(w, r)
		return
	}
	if !a.parseCSRFForm(w, r) {
		return
	}
	user, _ := r.Context().Value(userContextKey).(auth.User)
	if err := a.feeds.MarkFeedRead(r.Context(), user.ID, r.PathValue("id")); err != nil {
		if errors.Is(err, core.ErrFeedNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "mark feed read failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, redirectTarget(r), http.StatusSeeOther)
}

func (a *App) markAllRead(w http.ResponseWriter, r *http.Request) {
	if a.feeds == nil {
		http.NotFound(w, r)
		return
	}
	if !a.parseCSRFForm(w, r) {
		return
	}
	user, _ := r.Context().Value(userContextKey).(auth.User)
	if err := a.feeds.MarkAllRead(r.Context(), user.ID); err != nil {
		http.Error(w, "mark all read failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, redirectTarget(r), http.StatusSeeOther)
}

func (a *App) markItemRead(w http.ResponseWriter, r *http.Request) {
	if a.feeds == nil {
		http.NotFound(w, r)
		return
	}
	if !a.parseCSRFForm(w, r) {
		return
	}
	user, _ := r.Context().Value(userContextKey).(auth.User)
	if err := a.feeds.MarkRead(r.Context(), user.ID, r.PathValue("id")); err != nil {
		if errors.Is(err, core.ErrItemNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "mark read failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, redirectTarget(r), http.StatusSeeOther)
}

func (a *App) markItemUnread(w http.ResponseWriter, r *http.Request) {
	if a.feeds == nil {
		http.NotFound(w, r)
		return
	}
	if !a.parseCSRFForm(w, r) {
		return
	}
	user, _ := r.Context().Value(userContextKey).(auth.User)
	if err := a.feeds.MarkUnread(r.Context(), user.ID, r.PathValue("id")); err != nil {
		if errors.Is(err, core.ErrItemNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "mark unread failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, redirectTarget(r), http.StatusSeeOther)
}

func (a *App) setupForm(w http.ResponseWriter, r *http.Request) {
	required, err := a.setupRequired(r)
	if err != nil {
		http.Error(w, "setup check failed", http.StatusInternalServerError)
		return
	}
	if !required {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.renderPage(w, r, "setup.html", pageData{})
}

func (a *App) setup(w http.ResponseWriter, r *http.Request) {
	required, err := a.setupRequired(r)
	if err != nil {
		http.Error(w, "setup check failed", http.StatusInternalServerError)
		return
	}
	if !required {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !a.parseCSRFForm(w, r) {
		return
	}
	if a.setupToken != "" && subtle.ConstantTimeCompare([]byte(a.setupToken), []byte(r.FormValue("setup_token"))) != 1 {
		http.Error(w, "invalid setup token", http.StatusForbidden)
		return
	}
	result, err := a.auth.Setup(r.Context(), r.FormValue("email"), r.FormValue("password"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	setSessionCookie(w, result.Token, a.cookieSecure)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) loginForm(w http.ResponseWriter, r *http.Request) {
	if a.redirectToSetup(w, r) {
		return
	}
	notice := ""
	if r.URL.Query().Get("notice") == "password-reset" {
		notice = "Password reset. You can now log in."
	}
	a.renderPage(w, r, "login.html", pageData{Notice: notice, ResetEnabled: a.passwordResetEnabled()})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if a.redirectToSetup(w, r) {
		return
	}
	if !a.parseCSRFForm(w, r) {
		return
	}
	result, err := a.auth.Login(r.Context(), r.FormValue("email"), r.FormValue("password"))
	if errors.Is(err, auth.ErrInvalidCredentials) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "login failed", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, result.Token, a.cookieSecure)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) forgotPasswordForm(w http.ResponseWriter, r *http.Request) {
	if !a.passwordResetEnabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a.renderPage(w, r, "forgot-password.html", pageData{})
}

func (a *App) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	if !a.passwordResetEnabled() {
		http.NotFound(w, r)
		return
	}
	if !a.parseCSRFForm(w, r) {
		return
	}
	if err := a.auth.RequestPasswordReset(r.Context(), r.FormValue("email")); err != nil {
		a.log.Error("password reset request failed", "error", err)
	}
	w.Header().Set("Cache-Control", "no-store")
	a.renderPage(w, r, "forgot-password.html", pageData{Notice: "If that account exists, a reset link has been sent."})
}

func (a *App) resetPasswordForm(w http.ResponseWriter, r *http.Request) {
	if !a.passwordResetEnabled() {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		http.Error(w, "invalid reset link", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a.renderPage(w, r, "reset.html", pageData{ResetToken: token})
}

func (a *App) resetPassword(w http.ResponseWriter, r *http.Request) {
	if !a.passwordResetEnabled() {
		http.NotFound(w, r)
		return
	}
	if !a.parseCSRFForm(w, r) {
		return
	}
	if err := a.auth.ResetPassword(r.Context(), r.FormValue("token"), r.FormValue("password")); err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidResetToken):
			http.Error(w, "invalid or expired reset link", http.StatusBadRequest)
		case errors.Is(err, auth.ErrPasswordTooShort):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			a.log.Error("password reset failed", "error", err)
			http.Error(w, "password reset failed", http.StatusInternalServerError)
		}
		return
	}
	clearSessionCookie(w, a.cookieSecure)
	http.Redirect(w, r, "/login?notice=password-reset", http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if !a.parseCSRFForm(w, r) {
		return
	}
	if a.auth != nil {
		_ = a.auth.Logout(r.Context(), sessionToken(r))
	}
	clearSessionCookie(w, a.cookieSecure)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.auth == nil {
			next.ServeHTTP(w, r)
			return
		}
		user, _, err := a.auth.AuthenticateToken(r.Context(), sessionToken(r))
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *App) redirectToSetup(w http.ResponseWriter, r *http.Request) bool {
	required, err := a.setupRequired(r)
	if err != nil {
		http.Error(w, "setup check failed", http.StatusInternalServerError)
		return true
	}
	if required {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return true
	}
	return false
}

func (a *App) setupRequired(r *http.Request) (bool, error) {
	if a.auth == nil {
		return false, nil
	}
	return a.auth.SetupRequired(r.Context())
}

func (a *App) passwordResetEnabled() bool {
	return a.auth != nil && a.auth.PasswordResetEnabled()
}

func setSessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     sessionCookiePath,
		MaxAge:   int(auth.DefaultSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Path:     sessionCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func sessionToken(r *http.Request) string {
	cookie, err := r.Cookie(auth.CookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (a *App) renderPage(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	token, err := a.ensureCSRFToken(w, r)
	if err != nil {
		http.Error(w, "CSRF token failed", http.StatusInternalServerError)
		return
	}
	data.CSRFToken = token
	data.ReturnTo = r.URL.RequestURI()
	render(w, name, data)
}

func (a *App) ensureCSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
	if cookie, err := r.Cookie(csrfCookieName); err == nil && cookie.Value != "" {
		return cookie.Value, nil
	}

	token, err := auth.GenerateToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     sessionCookiePath,
		MaxAge:   int(auth.DefaultSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   a.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	return token, nil
}

func (a *App) parseCSRFForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return false
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return false
	}
	return true
}

func validCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return false
	}
	formToken := r.FormValue(csrfFormField)
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(formToken)) == 1
}

func render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self' http: https: data:; object-src 'none'; style-src 'self'")
		w.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func requestLogger(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Debug("request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
