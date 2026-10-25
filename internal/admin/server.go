package admin

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"github.com/gorilla/sessions"
	"golang.org/x/time/rate"

	"github.com/usememos/memogram/internal/config"
)

//go:embed web/*
var assets embed.FS

type Server struct {
	settings   *Settings
	cookies    *sessions.CookieStore
	loginLimit *rate.Limiter
}

func NewHandler(settings *Settings) (http.Handler, error) {
	key := make([]byte, 64)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	cookies := sessions.NewCookieStore(key[:32], key[32:])
	cookies.Options = &sessions.Options{Path: "/", MaxAge: 86400, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}
	s := &Server{settings: settings, cookies: cookies, loginLimit: rate.NewLimiter(rate.Every(time.Second), 10)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, map[string]bool{"setup_required": s.settings.AuthID() == "", "authenticated": s.authenticated(r)})
	})
	mux.HandleFunc("POST /api/setup", s.setup)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.Handle("GET /api/settings", s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { respond(w, http.StatusOK, s.settings.View()) })))
	mux.Handle("PUT /api/settings", s.requireAuth(http.HandlerFunc(s.save)))
	mux.Handle("POST /api/password", s.requireAuth(http.HandlerFunc(s.password)))
	files, err := fs.Sub(assets, "web")
	if err != nil {
		return nil, err
	}
	mux.Handle("GET /", http.FileServerFS(files))
	protected := http.NewCrossOriginProtection().Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		protected.ServeHTTP(w, r)
	}), nil
}

func (s *Server) authenticated(r *http.Request) bool {
	session, err := s.cookies.Get(r, "memogram_admin")
	if err != nil {
		return false
	}
	id, _ := session.Values["auth_id"].(string)
	return id != "" && id == s.settings.AuthID()
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authenticated(r) {
			respond(w, http.StatusUnauthorized, map[string]string{"error": "请先登录"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	session, _ := s.cookies.New(r, "memogram_admin")
	session.Values["auth_id"] = s.settings.AuthID()
	if err := session.Save(r, w); err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "保存登录状态失败"})
		return
	}
	respond(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimit.Allow() {
		respond(w, http.StatusTooManyRequests, map[string]string{"error": "操作太快，请稍后再试"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.settings.SetPassword("", body.Password, true); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.signIn(w, r)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimit.Allow() {
		respond(w, http.StatusTooManyRequests, map[string]string{"error": "操作太快，请稍后再试"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !s.settings.CheckPassword(body.Password) {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "密码不正确"})
		return
	}
	s.signIn(w, r)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	session, _ := s.cookies.New(r, "memogram_admin")
	session.Options.MaxAge = -1
	if err := session.Save(r, w); err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "退出登录失败"})
		return
	}
	respond(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Notebook  *config.Notebook  `json:"notebook"`
		APIKeys   map[string]string `json:"api_keys"`
		ResetKeys []string          `json:"reset_keys"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.settings.Save(body.Notebook, body.APIKeys, body.ResetKeys); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	respond(w, http.StatusOK, s.settings.View())
}

func (s *Server) password(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimit.Allow() {
		respond(w, http.StatusTooManyRequests, map[string]string{"error": "操作太快，请稍后再试"})
		return
	}
	var body struct {
		OldPassword string `json:"old_password"`
		Password    string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.settings.SetPassword(body.OldPassword, body.Password, false); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.signIn(w, r)
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "请求内容无效或超过大小限制"})
		return false
	}
	return true
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
