# Tesla Vehicle Onboarding Guide

This guide walks through all the steps required to provision a Tesla vehicle for use with this OAuth server.

## Overview

The Tesla Fleet API requires a multi-step setup process:
1. Create a Tesla Developer Account
2. Register an application
3. Generate cryptographic key pairs
4. Host your public key at a well-known endpoint
5. Register the public key with Tesla
6. Add the virtual key to your vehicle
7. Complete OAuth authorization

## Prerequisites

- A Tesla account with at least one vehicle
- A domain you control with HTTPS support
- Kubernetes cluster (or another way to host the OAuth server)

## Step 1: Create a Tesla Developer Account

1. Go to [developer.tesla.com](https://developer.tesla.com)
2. Sign in with your Tesla account
3. Accept the developer terms of service

## Step 2: Register an Application

1. In the Tesla Developer portal, click **Create Application**
2. Fill in the required fields:
   - **Application Name**: A descriptive name (e.g., "My Home Automation")
   - **Description**: Brief description of your application
   - **Purpose**: Select the appropriate purpose
   - **Origin URL**: Your domain (e.g., `https://tesla.example.com`)
   - **Redirect URI**: Your callback URL (e.g., `https://tesla.example.com/callback`)
3. Note down your **Client ID** and **Client Secret**

### Required Scopes

This application requires the following OAuth scopes:
- `openid` - Required for authentication
- `offline_access` - Required for refresh tokens
- `vehicle_device_data` - Read vehicle state and charge data
- `vehicle_cmds` - Send commands via signed protocol
- `vehicle_charging_cmds` - Control charging (start/stop)

## Step 3: Generate Key Pairs

Tesla requires EC (Elliptic Curve) key pairs using the secp256r1 (prime256v1) curve.

```bash
# Generate the private key
openssl ecparam -name prime256v1 -genkey -noout -out private-key.pem

# Extract the public key
openssl ec -in private-key.pem -pubout -out public-key.pem

# Create the Tesla-specific public key file
cp public-key.pem com.tesla.3p.public-key.pem
```

**Important**: Keep your `private-key.pem` secure. Never commit it to source control or share it publicly.

## Step 4: Host the Public Key

Tesla requires your public key to be served at a specific well-known URL:

```
https://<your-domain>/.well-known/appspecific/com.tesla.3p.public-key.pem
```

This OAuth server handles this automatically via the `/` route, which proxies requests to `/.well-known/appspecific/com.tesla.3p.public-key.pem`.

### Verify the Public Key Endpoint

After deployment, verify your public key is accessible:

```bash
curl https://tesla.example.com/.well-known/appspecific/com.tesla.3p.public-key.pem
```

You should see your PEM-encoded public key.

## Step 5: Register the Public Key with Tesla

1. Go to the [Tesla Developer portal](https://developer.tesla.com)
2. Navigate to your application
3. Under **Client Details**, find the **Domain** section
4. Tesla will automatically fetch and verify your public key from the well-known endpoint

The domain verification ensures that:
- Your server is accessible
- The public key is properly formatted
- The key matches your registered application domain

## Step 6: Add the Virtual Key to Your Vehicle

This is a critical step that authorizes your application to send commands to your vehicle.

### Option A: Using the Tesla App (Recommended)

1. Deploy and run this OAuth server
2. Navigate to your OAuth server in a browser
3. Click **Login with Tesla**
4. After successful OAuth, Tesla may prompt you to add the virtual key
5. Follow the in-app prompts to approve adding the key to your vehicle

### Option B: Using the Tesla Fleet API

After obtaining an access token, you can programmatically request virtual key pairing:

```bash
curl -X POST "https://fleet-api.prd.eu.vn.cloud.tesla.com/api/1/vehicles/{vehicle_id}/commands/virtual_key" \
  -H "Authorization: Bearer YOUR_ACCESS_TOKEN" \
  -H "Content-Type: application/json"
```

The vehicle owner will receive a notification in their Tesla app to approve the virtual key.

### Option C: In-Vehicle Approval

1. Sit in the vehicle
2. Go to **Controls > Safety > Security > Allow Mobile Access**
3. You may see a prompt to approve third-party keys

## Step 7: Complete OAuth Authorization

1. Navigate to your deployed OAuth server
2. Click **Login with Tesla**
3. Sign in with your Tesla credentials
4. Review and approve the requested permissions
5. You'll be redirected back with a valid access token

## Deployment Checklist

### Kubernetes Secrets

Create the required secrets:

```bash
# OAuth credentials
kubectl create secret generic tesla-oauth-secrets \
  --from-literal=client-id=YOUR_CLIENT_ID \
  --from-literal=client-secret=YOUR_CLIENT_SECRET \
  -n tesla

# Cryptographic keys
kubectl create secret generic tesla-keys \
  --from-file=public-key.pem=com.tesla.3p.public-key.pem \
  --from-file=private-key.pem=private-key.pem \
  -n tesla
```

### Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `TESLA_CLIENT_ID` | From Tesla Developer portal | `abc123...` |
| `TESLA_CLIENT_SECRET` | From Tesla Developer portal | `secret...` |
| `TESLA_REDIRECT_URI` | Must match app registration | `https://tesla.example.com/callback` |
| `TESLA_REGION` | Fleet API region | `na`, `eu`, or `cn` |
| `TESLA_PUBLIC_KEY_PATH` | Path to public key | `/app/keys/public-key.pem` |
| `TESLA_PRIVATE_KEY_PATH` | Path to private key | `/app/keys/private-key.pem` |

### Regional Endpoints

Choose the correct region based on where your Tesla account is registered:

| Region | Code | Fleet API URL |
|--------|------|---------------|
| North America | `na` | `https://fleet-api.prd.na.vn.cloud.tesla.com` |
| Europe | `eu` | `https://fleet-api.prd.eu.vn.cloud.tesla.com` |
| China | `cn` | `https://fleet-api.prd.cn.vn.cloud.tesla.com` |

## Troubleshooting

### "Vehicle not found" or 404 errors
- Ensure you're using the correct region
- Verify the VIN matches a vehicle on your account
- Check that the OAuth token has the required scopes

### "Command failed" or unauthorized errors
- The virtual key may not be installed on the vehicle
- Re-do Step 6 to add the virtual key
- Ensure the vehicle has mobile access enabled

### Public key not found by Tesla
- Verify the `.well-known` endpoint returns the correct key
- Check that your domain uses HTTPS with a valid certificate
- Ensure there are no redirects on the well-known path

### "Invalid state" on callback
- The OAuth state cookie may have expired
- Try the login flow again
- Check that cookies are being set correctly (HTTPS required for secure cookies)

### Vehicle commands timeout
- The vehicle may be in deep sleep
- Use the **Wake** button first, then retry the command
- Ensure the vehicle has cellular connectivity

## Security Considerations

- **Private Key**: Never expose your private key. Store it securely in Kubernetes secrets.
- **Client Secret**: Treat this like a password. Use secrets management.
- **Access Tokens**: Stored in HTTP-only cookies with 8-hour expiry.
- **CSRF Protection**: State parameter validates OAuth callbacks.

## Useful Links

- [Tesla Fleet API Documentation](https://developer.tesla.com/docs/fleet-api)
- [Tesla Developer Portal](https://developer.tesla.com)
- [Tesla Vehicle Command SDK](https://github.com/teslamotors/vehicle-command)
