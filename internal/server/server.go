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
	"strconv"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/spool-reader/spool/internal/auth"
	"github.com/spool-reader/spool/internal/config"
	"github.com/spool-reader/spool/internal/core"
	"github.com/spool-reader/spool/internal/feed"
)

const (
	sessionCookiePath = "/"
	csrfCookieName    = "spool_csrf"
	csrfFormField     = "csrf_token"
)

//go:embed templates/*.html
var templateFiles embed.FS

//go:embed assets/app.css
var appCSS []byte

var richTextPolicy = bluemonday.UGCPolicy()

var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"formatDate": formatDate,
	"richText":   richText,
}).ParseFS(templateFiles, "templates/*.html"))

type contextKey string

const userContextKey contextKey = "user"

const (
	latestItemLimit = 20
	firstPage       = 1
)

type App struct {
	auth  *auth.Service
	feeds *feed.Service
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
}

func New(cfg config.Config, log *slog.Logger, authSvc *auth.Service, feedSvc *feed.Service) *http.Server {
	return &http.Server{
		Addr:    cfg.Addr,
		Handler: NewMux(log, authSvc, feedSvc),
	}
}

func NewMux(log *slog.Logger, authSvc *auth.Service, feedSvc *feed.Service) http.Handler {
	if log == nil {
		log = slog.Default()
	}

	app := &App{auth: authSvc, feeds: feedSvc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /assets/app.css", stylesheet)
	mux.HandleFunc("GET /setup", app.setupForm)
	mux.HandleFunc("POST /setup", app.setup)
	mux.HandleFunc("GET /login", app.loginForm)
	mux.HandleFunc("POST /login", app.login)
	mux.HandleFunc("POST /logout", app.logout)
	mux.Handle("GET /", app.requireAuth(http.HandlerFunc(app.home)))
	mux.Handle("GET /feeds", app.requireAuth(http.HandlerFunc(app.feedList)))
	mux.Handle("GET /feeds/{id}", app.requireAuth(http.HandlerFunc(app.feedDetail)))
	mux.Handle("POST /feeds", app.requireAuth(http.HandlerFunc(app.addFeed)))
	mux.Handle("POST /feeds/{id}/refresh", app.requireAuth(http.HandlerFunc(app.refreshFeed)))

	return requestLogger(log, mux)
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

func feedNames(feeds []core.Feed) map[string]string {
	names := make(map[string]string, len(feeds))
	for _, feed := range feeds {
		names[feed.ID] = feed.Title
	}
	return names
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
		data.Feeds, err = a.feeds.ListFeeds(r.Context())
		if err != nil {
			http.Error(w, "feeds unavailable", http.StatusInternalServerError)
			return
		}
		data.Items, err = a.feeds.Latest(r.Context(), latestItemLimit+1, (page-firstPage)*latestItemLimit)
		if err != nil {
			http.Error(w, "items unavailable", http.StatusInternalServerError)
			return
		}
		if len(data.Items) > latestItemLimit {
			data.Items = data.Items[:latestItemLimit]
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
		data.Feeds, err = a.feeds.ListFeeds(r.Context())
		if err != nil {
			http.Error(w, "feeds unavailable", http.StatusInternalServerError)
			return
		}
	}
	a.renderPage(w, r, "feeds.html", data)
}

func (a *App) feedDetail(w http.ResponseWriter, r *http.Request) {
	if a.feeds == nil {
		http.NotFound(w, r)
		return
	}
	feed, err := a.feeds.Find(r.Context(), r.PathValue("id"))
	if errors.Is(err, core.ErrFeedNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "feed unavailable", http.StatusInternalServerError)
		return
	}
	items, err := a.feeds.Items(r.Context(), feed.ID)
	if err != nil {
		http.Error(w, "items unavailable", http.StatusInternalServerError)
		return
	}
	user, _ := r.Context().Value(userContextKey).(auth.User)
	a.renderPage(w, r, "feed.html", pageData{Email: user.Email, Notice: pageNotice(r), Feed: &feed, Items: items})
}

func (a *App) addFeed(w http.ResponseWriter, r *http.Request) {
	if a.feeds == nil {
		http.NotFound(w, r)
		return
	}
	if !a.parseCSRFForm(w, r) {
		return
	}
	if _, err := a.feeds.Add(r.Context(), r.FormValue("url")); err != nil {
		http.Error(w, "feed add failed", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/feeds?notice=added", http.StatusSeeOther)
}

func (a *App) refreshFeed(w http.ResponseWriter, r *http.Request) {
	if a.feeds == nil {
		http.NotFound(w, r)
		return
	}
	if !a.parseCSRFForm(w, r) {
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
	result, err := a.auth.Setup(r.Context(), r.FormValue("email"), r.FormValue("password"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	setSessionCookie(w, result.Token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) loginForm(w http.ResponseWriter, r *http.Request) {
	if a.redirectToSetup(w, r) {
		return
	}
	a.renderPage(w, r, "login.html", pageData{})
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
	setSessionCookie(w, result.Token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if !a.parseCSRFForm(w, r) {
		return
	}
	if a.auth != nil {
		_ = a.auth.Logout(r.Context(), sessionToken(r))
	}
	clearSessionCookie(w)
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

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     sessionCookiePath,
		MaxAge:   int(auth.DefaultSessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Path:     sessionCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
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
	token, err := ensureCSRFToken(w, r)
	if err != nil {
		http.Error(w, "CSRF token failed", http.StatusInternalServerError)
		return
	}
	data.CSRFToken = token
	render(w, name, data)
}

func ensureCSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
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
		SameSite: http.SameSiteLaxMode,
	})
	return token, nil
}

func (a *App) parseCSRFForm(w http.ResponseWriter, r *http.Request) bool {
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

func requestLogger(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Debug("request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
