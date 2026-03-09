package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
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
	audience     string
	publicKey    []byte

	regionAudience = map[string]string{
		"na": "https://fleet-api.prd.na.vn.cloud.tesla.com",
		"eu": "https://fleet-api.prd.eu.vn.cloud.tesla.com",
		"cn": "https://fleet-api.prd.cn.vn.cloud.tesla.com",
	}
)

func main() {
	clientID = os.Getenv("TESLA_CLIENT_ID")
	clientSecret = os.Getenv("TESLA_CLIENT_SECRET")
	redirectURI = os.Getenv("TESLA_REDIRECT_URI")

	if clientID == "" || clientSecret == "" || redirectURI == "" {
		log.Fatal("TESLA_CLIENT_ID, TESLA_CLIENT_SECRET, and TESLA_REDIRECT_URI must be set")
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

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

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
</body>
</html>`, token.Error, token.ErrorDesc)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Tesla OAuth Success</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 900px; margin: 50px auto; padding: 20px; }
.success { background: #efe; border: 1px solid #cfc; padding: 20px; border-radius: 8px; margin-bottom: 20px; }
.token-box { background: #f5f5f5; border: 1px solid #ddd; padding: 15px; border-radius: 4px; margin: 10px 0; position: relative; }
.token-box pre { margin: 0; white-space: pre-wrap; word-break: break-all; font-size: 12px; }
.token-box button { position: absolute; top: 10px; right: 10px; padding: 5px 10px; cursor: pointer; }
label { font-weight: bold; display: block; margin-top: 15px; }
.info { color: #666; font-size: 14px; }
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
<h1>Tesla OAuth Success</h1>
<div class="success">
<p>Authorization successful! State: <code>%s</code></p>
<p class="info">Expires in: %d seconds (%d hours)</p>
</div>

<label>Access Token:</label>
<div class="token-box">
<button onclick="copyToClipboard('access-token')">Copy</button>
<pre id="access-token">%s</pre>
</div>

<label>Refresh Token:</label>
<div class="token-box">
<button onclick="copyToClipboard('refresh-token')">Copy</button>
<pre id="refresh-token">%s</pre>
</div>

<label>ID Token:</label>
<div class="token-box">
<button onclick="copyToClipboard('id-token')">Copy</button>
<pre id="id-token">%s</pre>
</div>

</body>
</html>`, state, token.ExpiresIn, token.ExpiresIn/3600, token.AccessToken, token.RefreshToken, token.IDToken)
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
