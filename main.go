package main

import (
	"context"
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

	"github.com/teslamotors/vehicle-command/pkg/account"
	"github.com/teslamotors/vehicle-command/pkg/protocol"
	"github.com/teslamotors/vehicle-command/pkg/vehicle"
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

type Vehicle struct {
	ID          int64  `json:"id"`
	VehicleID   int64  `json:"vehicle_id"`
	VIN         string `json:"vin"`
	DisplayName string `json:"display_name"`
	State       string `json:"state"`
}

type VehiclesResponse struct {
	Response []Vehicle `json:"response"`
	Count    int       `json:"count"`
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
	privateKey   protocol.ECDHPrivateKey

	regionAudience = map[string]string{
		"na": "https://fleet-api.prd.na.vn.cloud.tesla.com",
		"eu": "https://fleet-api.prd.eu.vn.cloud.tesla.com",
		"cn": "https://fleet-api.prd.cn.vn.cloud.tesla.com",
	}
)

const (
	cookieName        = "tesla_token"
	cookieMaxAge      = 8 * 60 * 60 // 8 hours
	stateCookieName   = "tesla_state"
	stateCookieMaxAge = 10 * 60 // 10 minutes
)

// isSecure checks if the request is over HTTPS (directly or via proxy)
func isSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if r.Header.Get("X-Forwarded-Proto") == "https" {
		return true
	}
	return false
}

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

	privateKey, err = readPrivateKey()
	if err != nil {
		log.Fatalf("Failed to read private key: %v", err)
	}
	log.Printf("Loaded private key, public key: %x", privateKey.PublicBytes())

	http.HandleFunc("/.well-known/appspecific/com.tesla.3p.public-key.pem", handlePublicKey)
	http.HandleFunc("/api/vehicles/", handleVehicleCommand)
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
		// Fetch vehicles
		vehicles, vehicleErr := fetchVehicles(cookie.Value)

		var vehiclesHTML string
		if vehicleErr != nil {
			vehiclesHTML = fmt.Sprintf(`<div class="error"><p>Failed to fetch vehicles: %v</p></div>`, vehicleErr)
		} else if len(vehicles) == 0 {
			vehiclesHTML = `<p>No vehicles found.</p>`
		} else {
			vehiclesHTML = `<div class="vehicles">`
			for _, v := range vehicles {
				stateClass := "state-offline"
				if v.State == "online" {
					stateClass = "state-online"
				}
				vehiclesHTML += fmt.Sprintf(`
<div class="vehicle">
<h3>%s</h3>
<table>
<tr><td><strong>VIN:</strong></td><td><code>%s</code></td></tr>
<tr><td><strong>State:</strong></td><td><span class="%s">%s</span></td></tr>
<tr><td><strong>Vehicle ID:</strong></td><td>%d</td></tr>
</table>
<div class="vehicle-actions">
<button class="btn-cmd btn-flash" data-label="Flash Lights" onclick="sendCommand('%s', 'flash_lights', this)">Flash Lights</button>
<button class="btn-cmd btn-honk" data-label="Honk Horn" onclick="sendCommand('%s', 'honk_horn', this)">Honk Horn</button>
<button class="btn-cmd btn-lock" data-label="Lock" onclick="sendCommand('%s', 'lock', this)">Lock</button>
<button class="btn-cmd btn-unlock" data-label="Unlock" onclick="sendCommand('%s', 'unlock', this)">Unlock</button>
</div>
<div id="status-%s" class="cmd-status"></div>
</div>`, v.DisplayName, v.VIN, stateClass, v.State, v.VehicleID, v.VIN, v.VIN, v.VIN, v.VIN, v.VIN)
			}
			vehiclesHTML += `</div>`
		}

		_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Tesla OAuth</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
.success { background: #efe; border: 1px solid #cfc; padding: 20px; border-radius: 8px; margin-bottom: 20px; }
.error { background: #fee; border: 1px solid #fcc; padding: 15px; border-radius: 8px; margin-bottom: 20px; }
.btn { display: inline-block; padding: 12px 24px; font-size: 16px; text-decoration: none; border-radius: 6px; cursor: pointer; border: none; }
.btn-logout { background: #dc3545; color: white; }
.btn-logout:hover { background: #c82333; }
.token-box { background: #f5f5f5; border: 1px solid #ddd; padding: 15px; border-radius: 4px; margin: 10px 0; position: relative; }
.token-box pre { margin: 0; white-space: pre-wrap; word-break: break-all; font-size: 12px; }
.token-box button { position: absolute; top: 10px; right: 10px; padding: 5px 10px; cursor: pointer; }
.vehicles { margin: 20px 0; }
.vehicle { background: #f8f9fa; border: 1px solid #dee2e6; border-radius: 8px; padding: 15px; margin-bottom: 15px; }
.vehicle h3 { margin: 0 0 10px 0; color: #333; }
.vehicle table { width: 100%%; border-collapse: collapse; }
.vehicle td { padding: 5px 10px 5px 0; }
.state-online { color: #28a745; font-weight: bold; }
.state-offline { color: #6c757d; }
.vehicle-actions { margin-top: 15px; display: flex; gap: 10px; flex-wrap: wrap; }
.btn-cmd { padding: 8px 16px; border: none; border-radius: 4px; cursor: pointer; font-size: 14px; }
.btn-cmd:disabled { opacity: 0.6; cursor: not-allowed; }
.btn-flash { background: #ffc107; color: #000; }
.btn-flash:hover:not(:disabled) { background: #e0a800; }
.btn-honk { background: #17a2b8; color: #fff; }
.btn-honk:hover:not(:disabled) { background: #138496; }
.btn-lock { background: #28a745; color: #fff; }
.btn-lock:hover:not(:disabled) { background: #218838; }
.btn-unlock { background: #dc3545; color: #fff; }
.btn-unlock:hover:not(:disabled) { background: #c82333; }
.cmd-status { margin-top: 10px; padding: 8px 12px; border-radius: 4px; display: none; }
.cmd-success { background: #d4edda; color: #155724; display: block; }
.cmd-error { background: #f8d7da; color: #721c24; display: block; }
details { margin-top: 20px; }
summary { cursor: pointer; font-weight: bold; padding: 10px; background: #f5f5f5; border-radius: 4px; }
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
async function sendCommand(vin, command, btn) {
    const statusEl = document.getElementById('status-' + vin);
    const buttons = btn.parentElement.querySelectorAll('button');
    buttons.forEach(b => b.disabled = true);
    btn.innerText = 'Sending...';
    statusEl.className = 'cmd-status';
    statusEl.style.display = 'none';
    try {
        const resp = await fetch('/api/vehicles/' + vin + '/command/' + command, { method: 'POST' });
        const data = await resp.json();
        if (data.result) {
            statusEl.className = 'cmd-status cmd-success';
            statusEl.innerText = 'Command sent successfully!';
        } else {
            statusEl.className = 'cmd-status cmd-error';
            statusEl.innerText = 'Error: ' + data.error;
        }
    } catch (e) {
        statusEl.className = 'cmd-status cmd-error';
        statusEl.innerText = 'Error: ' + e.message;
    }
    buttons.forEach(b => b.disabled = false);
    btn.innerText = btn.dataset.label;
}
</script>
</head>
<body>
<h1>Tesla OAuth</h1>
<div class="success">
<p>You are logged in with a valid access token.</p>
</div>

<h2>Your Vehicles</h2>
%s

<details>
<summary>Access Token</summary>
<div class="token-box">
<button onclick="copyToClipboard('access-token')">Copy</button>
<pre id="access-token">%s</pre>
</div>
</details>

<p style="margin-top: 20px;"><a href="/logout" class="btn btn-logout">Logout</a></p>
</body>
</html>`, vehiclesHTML, cookie.Value)
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
	secure := isSecure(r)

	// Store state in cookie for CSRF protection
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		MaxAge:   stateCookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
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
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
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
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
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
		keyPath = "/app/keys/public-key.pem"
	}
	return os.ReadFile(keyPath)
}

func readPrivateKey() (protocol.ECDHPrivateKey, error) {
	keyPath := os.Getenv("TESLA_PRIVATE_KEY_PATH")
	if keyPath == "" {
		keyPath = "/app/keys/private-key.pem"
	}
	return protocol.LoadPrivateKey(keyPath)
}

func generateState() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

func fetchVehicles(accessToken string) ([]Vehicle, error) {
	req, err := http.NewRequest("GET", audience+"/api/1/vehicles", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch vehicles: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var vehiclesResp VehiclesResponse
	if err := json.Unmarshal(body, &vehiclesResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return vehiclesResp.Response, nil
}

func handleVehicleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cookie, err := r.Cookie(cookieName)
	if err != nil || cookie.Value == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	accessToken := cookie.Value

	// Parse URL: /api/vehicles/{vin}/command/{command}
	path := strings.TrimPrefix(r.URL.Path, "/api/vehicles/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[1] != "command" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	vin := parts[0]
	command := parts[2]

	w.Header().Set("Content-Type", "application/json")

	var cmdErr error
	switch command {
	case "flash_lights":
		cmdErr = executeVehicleCommand(r.Context(), accessToken, vin, func(v *vehicle.Vehicle) error {
			return v.FlashLights(r.Context())
		})
	case "honk_horn":
		cmdErr = executeVehicleCommand(r.Context(), accessToken, vin, func(v *vehicle.Vehicle) error {
			return v.HonkHorn(r.Context())
		})
	case "lock":
		cmdErr = executeVehicleCommand(r.Context(), accessToken, vin, func(v *vehicle.Vehicle) error {
			return v.Lock(r.Context())
		})
	case "unlock":
		cmdErr = executeVehicleCommand(r.Context(), accessToken, vin, func(v *vehicle.Vehicle) error {
			return v.Unlock(r.Context())
		})
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": false,
			"error":  fmt.Sprintf("unknown command: %s", command),
		})
		return
	}

	if cmdErr != nil {
		log.Printf("Command %s failed for VIN %s: %v", command, vin, cmdErr)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": false,
			"error":  cmdErr.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"result": true,
	})
}

func executeVehicleCommand(ctx context.Context, accessToken, vin string, cmd func(*vehicle.Vehicle) error) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	acct, err := account.New(accessToken, "tesla-oauth")
	if err != nil {
		return fmt.Errorf("failed to create account: %w", err)
	}

	car, err := acct.GetVehicle(ctx, vin, privateKey, nil)
	if err != nil {
		return fmt.Errorf("failed to get vehicle: %w", err)
	}
	defer car.Disconnect()

	if err := car.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	if err := car.StartSession(ctx, nil); err != nil {
		return fmt.Errorf("failed to start session: %w", err)
	}

	return cmd(car)
}
