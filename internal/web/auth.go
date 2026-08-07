package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// cookieName is the single session cookie this app sets. There is no
// server-side session store: the cookie itself carries a signed expiry
// (see signSession/verifySession).
const cookieName = "ll_session"

// sessionDuration is how long a successful /login grants access for.
const sessionDuration = 30 * 24 * time.Hour

// signSession returns an opaque cookie value binding expiryUnix to secret
// via HMAC-SHA256, so the server can validate it later without storing any
// session state: base64(expiryUnix + "." + hex(hmac_sha256(secret, expiryUnix))).
func signSession(secret string, expiryUnix int64) string {
	exp := strconv.FormatInt(expiryUnix, 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(exp))
	sig := hex.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(exp + "." + sig))
}

// verifySession reports whether value is a signSession output for secret
// whose expiry has not yet passed.
func verifySession(secret, value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return false
	}
	exp, sig, ok := strings.Cut(string(raw), ".")
	if !ok {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(exp))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return false
	}
	expUnix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() <= expUnix
}

// validBearer reports whether r carries "Authorization: Bearer <secret>"
// matching secret via a constant-time comparison.
func validBearer(r *http.Request, secret string) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	token := strings.TrimPrefix(auth, prefix)
	return subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1
}

// isAuthExempt reports whether r may bypass auth entirely: the health
// check, the login page itself, and static assets (the login page needs
// its CSS, and static assets carry nothing sensitive).
func isAuthExempt(r *http.Request) bool {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		return true
	case (r.Method == http.MethodGet || r.Method == http.MethodPost) && r.URL.Path == "/login":
		return true
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/static/"):
		return true
	default:
		return false
	}
}

// isMutating reports whether m is a method that changes state, and so is
// subject to the CSRF same-origin check for cookie-authenticated requests.
func isMutating(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// sameOriginRequest reports whether r looks like it originated from this
// same site, using Sec-Fetch-Site when present (modern browsers always
// send it) and falling back to Origin. Requests with neither header are
// allowed: htmx and browser form posts always send at least one of these
// on a cross-origin request, so their absence means same-origin (or a
// non-browser client, which authenticates via bearer token instead and
// skips this check entirely).
func sameOriginRequest(r *http.Request) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" {
		return sfs == "same-origin" || sfs == "none"
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return u.Host == r.Host
	}
	return true
}

// safeNext validates a user-supplied ?next= redirect target: it must be a
// single site-relative path, never an absolute or protocol-relative URL
// (which would turn login into an open redirect). Backslashes are
// rejected outright: several browsers normalize a leading "/\" the same
// way as "//" when resolving a Location header, which would otherwise
// let "/\evil.com" slip past the "//" check as a scheme-relative URL to
// evil.com. No legitimate in-app path ever contains one.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsRune(next, '\\') {
		return "/"
	}
	return next
}

// requestedNext builds the ?next= value for an unauthenticated request
// being bounced to /login, so a successful login returns the user where
// they were headed.
func requestedNext(r *http.Request) string {
	next := r.URL.Path
	if r.URL.RawQuery != "" {
		next += "?" + r.URL.RawQuery
	}
	return safeNext(next)
}

// authMiddleware wraps next with the single-secret auth gate described in
// docs/pitches/hosted-lineleader.md ("Lock the front door"): bearer tokens
// for CLI/API callers, a signed session cookie for browsers, and a
// same-origin check on cookie-authenticated mutations.
func authMiddleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isAuthExempt(r) {
				next.ServeHTTP(w, r)
				return
			}

			// CLI/API path: presence of Authorization decides bearer auth
			// outright, success or failure — no fallback to the cookie flow.
			if r.Header.Get("Authorization") != "" {
				if !validBearer(r, secret) {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// Browser path: signed session cookie.
			authed := false
			if c, err := r.Cookie(cookieName); err == nil {
				authed = verifySession(secret, c.Value)
			}
			if !authed {
				if r.Header.Get("HX-Request") != "" {
					// Never swap a login form into an htmx fragment target —
					// force a full-page redirect instead (pitch risk item).
					w.Header().Set("HX-Redirect", "/login")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				redirectTo := "/login?next=" + url.QueryEscape(requestedNext(r))
				http.Redirect(w, r, redirectTo, http.StatusSeeOther)
				return
			}

			if isMutating(r.Method) && !sameOriginRequest(r) {
				http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// authHandlers serves /login and /logout.
type authHandlers struct {
	tmpl          *template.Template
	secret        string
	secureCookies bool
}

type loginView struct {
	Next string
	Err  string
}

func (h *authHandlers) renderLogin(w http.ResponseWriter, status int, next, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = h.tmpl.ExecuteTemplate(w, "login_page", loginView{Next: safeNext(next), Err: errMsg})
}

// loginPage handles GET /login.
func (h *authHandlers) loginPage(w http.ResponseWriter, r *http.Request) {
	h.renderLogin(w, http.StatusOK, r.URL.Query().Get("next"), "")
}

// loginSubmit handles POST /login: constant-time password check, then a
// signed session cookie and a redirect to ?next (or /).
func (h *authHandlers) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderLogin(w, http.StatusBadRequest, "", "Bad request.")
		return
	}
	next := safeNext(r.FormValue("next"))
	password := r.FormValue("password")
	if subtle.ConstantTimeCompare([]byte(password), []byte(h.secret)) != 1 {
		h.renderLogin(w, http.StatusUnauthorized, next, "Incorrect password.")
		return
	}

	expiry := time.Now().Add(sessionDuration)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    signSession(h.secret, expiry.Unix()),
		Path:     "/",
		Expires:  expiry,
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// logout handles POST /logout: clears the session cookie.
func (h *authHandlers) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
