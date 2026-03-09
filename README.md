# Tesla OAuth Server

A simple OAuth server for Tesla Fleet API authentication. Provides a login flow with secure cookie storage, serves your public key at the well-known endpoint, and handles the OAuth callback to exchange authorization codes for tokens.

## Prerequisites

- Go 1.26+
- Kubernetes cluster with Gateway API support
- Tesla Developer account with a registered application

## Configuration

You'll need the following from your Tesla Developer application:

- **Client ID**
- **Client Secret**
- **Public Key** (`com.tesla.3p.public-key.pem`)

## Build

### Local

```bash
go build -o tesla-oauth .
```

### Container Image (ko)

This project uses [ko](https://ko.build/) for building container images. Images are automatically built and published to GitHub Container Registry via GitHub Actions.

```bash
# Build locally
ko build --local .

# Build and push (CI does this automatically)
KO_DOCKER_REPO=ghcr.io/loafoe/tesla-oauth ko build --bare .
```

Pre-built images are available at: `ghcr.io/loafoe/tesla-oauth`

## Deploy to Kubernetes

### 1. Create the namespace

```bash
kubectl create namespace tesla
```

### 2. Create the secrets

Create the OAuth credentials secret:

```bash
kubectl create secret generic tesla-oauth-secrets \
  --from-literal=client-id=YOUR_CLIENT_ID \
  --from-literal=client-secret=YOUR_CLIENT_SECRET \
  -n tesla
```

Create the public key secret:

```bash
kubectl create secret generic tesla-public-key \
  --from-file=com.tesla.3p.public-key.pem \
  -n tesla
```

### 3. Deploy the application

```bash
kubectl apply -f deployment.yaml -n tesla
```

## Endpoints

| Path | Description |
|------|-------------|
| `/` | Login page - shows login button or current token if authenticated |
| `/login` | Starts OAuth2 flow with Tesla |
| `/logout` | Clears the access token cookie |
| `/callback` | OAuth callback handler - exchanges auth code for tokens |
| `/health` | Health check (returns "ok") |
| `/.well-known/appspecific/com.tesla.3p.public-key.pem` | Serves your Tesla public key |

## Environment Variables

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `TESLA_CLIENT_ID` | Your Tesla app client ID | Yes | - |
| `TESLA_CLIENT_SECRET` | Your Tesla app client secret | Yes | - |
| `TESLA_REDIRECT_URI` | OAuth redirect URI (must match Tesla app config) | Yes | - |
| `TESLA_REGION` | Fleet API region: `na`, `eu`, or `cn` | No | `eu` |
| `TESLA_SCOPES` | OAuth scopes to request | No | `openid offline_access vehicle_device_data vehicle_charging_cmds` |
| `TESLA_PUBLIC_KEY_PATH` | Path to public key file | No | `/app/com.tesla.3p.public-key.pem` |
| `PORT` | Server port | No | `8080` |

## Security

- Access tokens are stored in secure HTTP-only cookies with `SameSite=Strict`
- CSRF protection via state parameter stored in a separate cookie
- Cookie expiry matches token expiry (max 8 hours)

## Regional Fleet API Endpoints

| Region | Audience URL |
|--------|-------------|
| `na` | `https://fleet-api.prd.na.vn.cloud.tesla.com` |
| `eu` | `https://fleet-api.prd.eu.vn.cloud.tesla.com` |
| `cn` | `https://fleet-api.prd.cn.vn.cloud.tesla.com` |
