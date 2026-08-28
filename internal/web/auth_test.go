package web

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
)

func newAuthTestServer(t *testing.T, secret string, secureCookies bool) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	srv := NewServer(Options{
		Charts:        []*dvc.ResortChart{minimalChart()},
		Config:        dvc.Config{},
		ConfigPath:    filepath.Join(dir, "config.json"),
		Ledger:        ledger.OpenTest(t),
		AuthSecret:    secret,
		SecureCookies: secureCookies,
	})
	return httptest.NewServer(srv)
}

// noRedirectClient never follows redirects, so tests can inspect the
// redirect response itself.
func noRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func TestAuth_EmptySecret_NoAuthApplied(t *testing.T) {
	ts := newTestServer(t) // AuthSecret unset
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200 (no auth configured)", resp.StatusCode)
	}
}

func TestAuth_UnauthenticatedGetIndex_RedirectsToLogin(t *testing.T) {
	ts := newAuthTestServer(t, "s3cret", false)
	defer ts.Close()

	client := noRedirectClient()
	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET / status = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/login") {
		t.Fatalf("Location = %q, want prefix /login", loc)
	}
}

func TestAuth_UnauthenticatedHtmxGet_Returns401WithHXRedirect(t *testing.T) {
	ts := newAuthTestServer(t, "s3cret", false)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("HX-Redirect"); got != "/login" {
		t.Fatalf("HX-Redirect = %q, want /login", got)
	}
}

func TestAuth_HealthzStaticLogin_ReachableUnauthenticated(t *testing.T) {
	ts := newAuthTestServer(t, "s3cret", false)
	defer ts.Close()

	for _, path := range []string{"/healthz", "/static/style.css", "/login"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, resp.StatusCode)
		}
	}
}

func TestAuth_LoginWrongPassword_NoCookie(t *testing.T) {
	ts := newAuthTestServer(t, "s3cret", false)
	defer ts.Close()

	client := noRedirectClient()
	resp, err := client.PostForm(ts.URL+"/login", url.Values{"password": {"nope"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "ll_session" {
			t.Fatalf("wrong password set a session cookie")
		}
	}
}

func TestAuth_LoginCorrectPassword_SetsCookieThenAuthenticated(t *testing.T) {
	ts := newAuthTestServer(t, "s3cret", false)
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	resp, err := client.PostForm(ts.URL+"/login", url.Values{"password": {"s3cret"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("after-redirect status = %d, want 200", resp.StatusCode)
	}

	resp2, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200 once logged in", resp2.StatusCode)
	}
}

func TestAuth_BearerCorrectSecret_Allowed(t *testing.T) {
	ts := newAuthTestServer(t, "s3cret", false)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestAuth_BearerWrongSecret_Unauthorized(t *testing.T) {
	ts := newAuthTestServer(t, "s3cret", false)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAuth_CookieAuthPost_EvilOrigin_Forbidden(t *testing.T) {
	ts := newAuthTestServer(t, "s3cret", false)
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	resp, err := client.PostForm(ts.URL+"/login", url.Values{"password": {"s3cret"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/trips", strings.NewReader("budget=200"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example")
	for _, c := range jar.Cookies(req.URL) {
		req.AddCookie(c)
	}
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp2.StatusCode)
	}
}

func TestAuth_BearerPost_EvilOrigin_Allowed(t *testing.T) {
	ts := newAuthTestServer(t, "s3cret", false)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/trips", strings.NewReader("budget=200"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (bearer exempt from Origin check)", resp.StatusCode)
	}
}

func TestAuth_ExpiredOrGarbageCookie_TreatedAsUnauthenticated(t *testing.T) {
	ts := newAuthTestServer(t, "s3cret", false)
	defer ts.Close()

	cases := []string{
		"garbage-not-base64!!",
		signSession("s3cret", -1), // already-expired, correctly signed
		signSession("wrong-secret", 999999999999),
	}
	for _, val := range cases {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: "ll_session", Value: val})
		client := noRedirectClient()
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("cookie %q: status = %d, want 303 (unauthenticated)", val, resp.StatusCode)
		}
	}
}

// TestSafeNext_RejectsBackslashPaths guards against a browser-normalization
// bypass: some browsers treat a leading "/\" in a Location header the same
// as "//" when resolving it, so "/\evil.com" would otherwise slip past the
// "//" protocol-relative check as a scheme-relative redirect to evil.com.
func TestSafeNext_RejectsBackslashPaths(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`/\evil.com`, "/"},
		{`/a\b`, "/"},
		{"//evil.com", "/"},    // pre-existing protocol-relative guard, kept as a regression check
		{"/ledger", "/ledger"}, // legitimate in-app path still passes through
	}
	for _, c := range cases {
		if got := safeNext(c.in); got != c.want {
			t.Errorf("safeNext(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestAuth_LoginBackslashNext_DoesNotRedirectOffSite exercises the same
// guard end-to-end through POST /login, confirming a backslash-prefixed
// next never reaches the Location header.
func TestAuth_LoginBackslashNext_DoesNotRedirectOffSite(t *testing.T) {
	ts := newAuthTestServer(t, "s3cret", false)
	defer ts.Close()

	client := noRedirectClient()
	resp, err := client.PostForm(ts.URL+"/login", url.Values{
		"password": {"s3cret"},
		"next":     {`/\evil.com`},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("Location = %q, want /", loc)
	}
}
