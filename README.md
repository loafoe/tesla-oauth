# Tesla OAuth Server

A simple OAuth callback server for Tesla Fleet API authentication. Serves your public key at the well-known endpoint and handles the OAuth callback to exchange authorization codes for tokens.

## Prerequisites

- Go 1.26+
- Docker
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

### Docker

```bash
docker build -t tesla-oauth:latest .
```

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
| `/` | Health check (returns "ok") |
| `/.well-known/appspecific/com.tesla.3p.public-key.pem` | Serves your Tesla public key |
| `/callback` | OAuth callback handler - exchanges auth code for tokens |

## Environment Variables

| Variable | Description | Required |
|----------|-------------|----------|
| `TESLA_CLIENT_ID` | Your Tesla app client ID | Yes |
| `TESLA_CLIENT_SECRET` | Your Tesla app client secret | Yes |
| `TESLA_REDIRECT_URI` | OAuth redirect URI (must match Tesla app config) | Yes |
| `PORT` | Server port (default: 8080) | No |
