package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	Error        string `json:"error,omitempty"`
	ErrorDesc    string `json:"error_description,omitempty"`
}

var (
	clientID     string
	clientSecret string
	redirectURI  string
	tokenURL     = "https://fleet-auth.prd.vn.cloud.tesla.com/oauth2/v3/token"
	authorizeURL = "https://auth.tesla.com/oauth2/v3/authorize"
	audience     string
	scopes       string
	publicKey    []byte

	regionAudience = map[string]string{
		"na": "https://fleet-api.prd.na.vn.cloud.tesla.com",
		"eu": "https://fleet-api.prd.eu.vn.cloud.tesla.com",
		"cn": "https://fleet-api.prd.cn.vn.cloud.tesla.com",
	}
)

const (
	cookieName       = "tesla_token"
	cookieMaxAge     = 8 * 60 * 60 // 8 hours
	stateCookieName  = "tesla_state"
	stateCookieMaxAge = 10 * 60 // 10 minutes
)

func main() {
	clientID = os.Getenv("TESLA_CLIENT_ID")
	clientSecret = os.Getenv("TESLA_CLIENT_SECRET")
	redirectURI = os.Getenv("TESLA_REDIRECT_URI")

	if clientID == "" || clientSecret == "" || redirectURI == "" {
		log.Fatal("TESLA_CLIENT_ID, TESLA_CLIENT_SECRET, and TESLA_REDIRECT_URI must be set")
	}

	scopes = os.Getenv("TESLA_SCOPES")
	if scopes == "" {
		scopes = "openid offline_access vehicle_device_data vehicle_charging_cmds"
	}

	region := os.Getenv("TESLA_REGION")
	if region == "" {
		region = "eu"
	}
	var ok bool
	audience, ok = regionAudience[region]
	if !ok {
		log.Fatalf("Invalid TESLA_REGION: %s (valid: na, eu, cn)", region)
	}
	log.Printf("Using Tesla Fleet API region: %s", region)

	var err error
	publicKey, err = readPublicKey()
	if err != nil {
		log.Fatalf("Failed to read public key: %v", err)
	}

	http.HandleFunc("/.well-known/appspecific/com.tesla.3p.public-key.pem", handlePublicKey)
	http.HandleFunc("/callback", handleCallback)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/logout", handleLogout)
	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/", handleRoot)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting server on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func handlePublicKey(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/x-pem-file")
	_, _ = w.Write(publicKey)
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok"))
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	cookie, err := r.Cookie(cookieName)
	hasValidToken := err == nil && cookie.Value != ""

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if hasValidToken {
		_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Tesla OAuth</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
.success { background: #efe; border: 1px solid #cfc; padding: 20px; border-radius: 8px; margin-bottom: 20px; }
.btn { display: inline-block; padding: 12px 24px; font-size: 16px; text-decoration: none; border-radius: 6px; cursor: pointer; border: none; }
.btn-logout { background: #dc3545; color: white; }
.btn-logout:hover { background: #c82333; }
.token-box { background: #f5f5f5; border: 1px solid #ddd; padding: 15px; border-radius: 4px; margin: 10px 0; position: relative; }
.token-box pre { margin: 0; white-space: pre-wrap; word-break: break-all; font-size: 12px; }
.token-box button { position: absolute; top: 10px; right: 10px; padding: 5px 10px; cursor: pointer; }
</style>
<script>
function copyToClipboard(id) {
    const text = document.getElementById(id).innerText;
    navigator.clipboard.writeText(text).then(() => {
        const btn = event.target;
        btn.innerText = 'Copied!';
        setTimeout(() => btn.innerText = 'Copy', 2000);
    });
}
</script>
</head>
<body>
<h1>Tesla OAuth</h1>
<div class="success">
<p>You are logged in with a valid access token.</p>
</div>

<label><strong>Access Token:</strong></label>
<div class="token-box">
<button onclick="copyToClipboard('access-token')">Copy</button>
<pre id="access-token">%s</pre>
</div>

<p><a href="/logout" class="btn btn-logout">Logout</a></p>
</body>
</html>`, cookie.Value)
	} else {
		_, _ = fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>Tesla OAuth</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; text-align: center; }
.btn { display: inline-block; padding: 12px 24px; font-size: 16px; text-decoration: none; border-radius: 6px; cursor: pointer; }
.btn-login { background: #e82127; color: white; }
.btn-login:hover { background: #c71d23; }
</style>
</head>
<body>
<h1>Tesla OAuth</h1>
<p>Sign in with your Tesla account to get an access token.</p>
<p><a href="/login" class="btn btn-login">Login with Tesla</a></p>
</body>
</html>`)
	}
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	state := generateState()

	// Store state in cookie for CSRF protection
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		MaxAge:   stateCookieMaxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
	})

	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", scopes)
	params.Set("state", state)
	params.Set("locale", "en-US")
	params.Set("prompt", "login")

	authURL := authorizeURL + "?" + params.Encode()
	http.Redirect(w, r, authURL, http.StatusFound)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	// Verify state for CSRF protection
	stateCookie, err := r.Cookie(stateCookieName)
	if err != nil || stateCookie.Value != state {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>Tesla OAuth Error</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
.error { background: #fee; border: 1px solid #fcc; padding: 20px; border-radius: 8px; }
</style>
</head>
<body>
<h1>Authorization Error</h1>
<div class="error">
<p><strong>Error:</strong> Invalid state parameter (CSRF protection)</p>
<p>Please <a href="/">try again</a>.</p>
</div>
</body>
</html>`)
		return
	}

	// Clear state cookie
	http.SetCookie(w, &http.Cookie{
		Name:   stateCookieName,
		Value:  "",
		MaxAge: -1,
		Path:   "/",
	})

	if code == "" {
		errorMsg := r.URL.Query().Get("error")
		errorDesc := r.URL.Query().Get("error_description")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Tesla OAuth Error</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
.error { background: #fee; border: 1px solid #fcc; padding: 20px; border-radius: 8px; }
</style>
</head>
<body>
<h1>Authorization Error</h1>
<div class="error">
<p><strong>Error:</strong> %s</p>
<p><strong>Description:</strong> %s</p>
</div>
<p><a href="/">Back to home</a></p>
</body>
</html>`, errorMsg, errorDesc)
		return
	}

	token, err := exchangeCode(code)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Token Exchange Error</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
.error { background: #fee; border: 1px solid #fcc; padding: 20px; border-radius: 8px; }
</style>
</head>
<body>
<h1>Token Exchange Error</h1>
<div class="error"><p>%v</p></div>
<p><a href="/">Back to home</a></p>
</body>
</html>`, err)
		return
	}

	if token.Error != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Tesla OAuth Error</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
.error { background: #fee; border: 1px solid #fcc; padding: 20px; border-radius: 8px; }
</style>
</head>
<body>
<h1>Token Error</h1>
<div class="error">
<p><strong>Error:</strong> %s</p>
<p><strong>Description:</strong> %s</p>
</div>
<p><a href="/">Back to home</a></p>
</body>
</html>`, token.Error, token.ErrorDesc)
		return
	}

	// Calculate cookie expiry (use token expiry or default max age, whichever is shorter)
	maxAge := cookieMaxAge
	if token.ExpiresIn > 0 && token.ExpiresIn < maxAge {
		maxAge = token.ExpiresIn
	}

	// Set secure cookie with access token
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token.AccessToken,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
		Expires:  time.Now().Add(time.Duration(maxAge) * time.Second),
	})

	http.Redirect(w, r, "/", http.StatusFound)
}

func exchangeCode(code string) (*TokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("audience", audience)

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var token TokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w (body: %s)", err, string(body))
	}

	return &token, nil
}

func readPublicKey() ([]byte, error) {
	keyPath := os.Getenv("TESLA_PUBLIC_KEY_PATH")
	if keyPath == "" {
		keyPath = "/app/com.tesla.3p.public-key.pem"
	}
	return os.ReadFile(keyPath)
}

func generateState() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}
