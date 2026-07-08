#!/usr/bin/env bash
# Creates GitHub issues for deploying ping to Hetzner/Coolify production.
# Prereqs: `gh auth login` completed. Run from repo root: ./hack/create-deploy-issues.sh
# Idempotent: labels upserted, existing issues skipped by title match.
set -euo pipefail

command -v gh >/dev/null 2>&1 || { echo "error: gh CLI not installed"; exit 1; }

REPO=$(gh repo view --json nameWithOwner -q .nameWithOwner)
echo "repo: $REPO"

echo "==> labels"
while read -r name color; do
  gh label create "$name" --color "$color" --force >/dev/null
  echo "    $name"
done <<'EOF'
deploy F4564E
infra 6E7781
size:S C2E0C6
size:M FBCA04
size:L D93F0B
backend 1F6FEB
frontend 3178C6
blocked D73A4A
EOF

echo "==> issues"

create_issue() {
  local title="$1" body="$2" labels="$3"
  if gh issue list --limit 300 --state all --json title -q ".[].title" | grep -qF "$title"; then
    echo "    SKIP: $title (already exists)"
    return
  fi
  gh issue create --title "$title" --body "$body" --label "$labels"
  echo "    CREATED: $title"
}

create_issue \
  "DEPLOY-1: Create combined Dockerfile (Go + Next.js + Caddy)" \
  "## Summary
Multi-stage Dockerfile following the shrt pattern:
1. **Stage 1** — build Go backend (golang:1.26-alpine), install golang-migrate
2. **Stage 2** — build Next.js frontend (node:20-alpine, standalone output)
3. **Stage 3** — runtime (caddy:2-alpine + node), Caddy routes traffic

## Caddy routing
- \`/api/v1/*\` → Go backend (:8080)
- \`/health\` → Go backend
- \`/p/*\` → Go backend (heartbeat/probe endpoints)
- \`/_next/*\`, known frontend routes → Next.js (:3000)
- Fallback → Go backend

## Start script
- Write JWT keys from env vars (fallback if no volume mount)
- Run DB migrations (\`migrate -path /migrations -database \$DATABASE_URL up\`)
- Start Next.js, Go backend, Caddy (foreground)

## AC
- [ ] Builds successfully locally (\`docker build .\`)
- [ ] Health endpoint returns 200
- [ ] Frontend pages load
- [ ] API routes respond correctly" \
  "deploy,infra,backend,frontend,size:M"

create_issue \
  "DEPLOY-2: Configure Cloudflare DNS for ping.sukhera.dev" \
  "## Summary
Add A record for \`ping.sukhera.dev\` → \`167.233.232.137\` in Cloudflare.

## Details
- DNS only mode (grey cloud) — Let's Encrypt needs direct access for cert issuance via Coolify
- Same pattern as scrt.sukhera.dev and shrt.sukhera.dev

## AC
- [ ] \`dig ping.sukhera.dev\` resolves to VPS IP
- [ ] HTTPS works after Coolify issues cert" \
  "deploy,infra,size:S"

create_issue \
  "DEPLOY-3: Generate JWT keypair on VPS" \
  "## Summary
Generate RSA keypair for ping's JWT auth, stored on the VPS and mounted as a Docker volume.

## Steps
\`\`\`bash
ssh root@167.233.232.137
mkdir -p /data/coolify/ping-keys
openssl genrsa -out /data/coolify/ping-keys/private.pem 2048
openssl rsa -in /data/coolify/ping-keys/private.pem -pubout -out /data/coolify/ping-keys/public.pem
chmod 600 /data/coolify/ping-keys/private.pem
chmod 644 /data/coolify/ping-keys/public.pem
\`\`\`

## Coolify volume mount
Source: \`/data/coolify/ping-keys\` → Target: \`/keys\`

## AC
- [ ] Keys exist on VPS at /data/coolify/ping-keys/
- [ ] Volume mount configured in Coolify
- [ ] Container can read the keys at /keys/private.pem and /keys/public.pem" \
  "deploy,infra,size:S"

create_issue \
  "DEPLOY-4: Create Coolify app + configure env vars" \
  "## Summary
Create the ping application in Coolify, connect to GitHub repo, and set all required environment variables.

## Required env vars
\`\`\`
PING_PORT=8080
PING_ENV=production
PING_BASE_URL=https://ping.sukhera.dev
CORS_ALLOWED_ORIGIN=https://ping.sukhera.dev
DATABASE_URL=postgres://<user>:<pass>@<host>:5432/ping?sslmode=disable
REDIS_URL=redis://default:<pass>@<host>:6379/2
JWT_PRIVATE_KEY_PATH=/keys/private.pem
JWT_PUBLIC_KEY_PATH=/keys/public.pem
JWT_ACCESS_TTL=15m
JWT_REFRESH_TTL=720h
REGISTRATION_OPEN=true
RETENTION_DAYS=90
API_URL=http://localhost:8080
\`\`\`

## Notes
- Create a new \`ping\` database in the existing Postgres instance (or use Coolify's Postgres provisioning)
- Redis DB 2 (scrt=0, shrt=1, ping=2)
- SMTP vars are optional — alerts won't send until email vendor is set up (separate issue)
- Add \`coolify\` to Network Aliases for DNS resolution

## AC
- [ ] App created in Coolify connected to sukhera/ping repo
- [ ] All env vars set
- [ ] Volume mount for JWT keys configured
- [ ] Postgres database created" \
  "deploy,infra,size:M"

create_issue \
  "DEPLOY-5: Deploy and smoke test" \
  "## Summary
Trigger first deploy in Coolify and verify everything works.

## Smoke test checklist
- [ ] \`https://ping.sukhera.dev/health\` returns 200
- [ ] Homepage loads (Next.js)
- [ ] Register a user account
- [ ] Login works
- [ ] Create a heartbeat monitor
- [ ] Create an HTTP monitor (pointing at scrt.sukhera.dev/health)
- [ ] Dashboard shows monitors
- [ ] Heartbeat ping endpoint \`/p/<slug>\` returns 200
- [ ] API keys page works

## Known considerations
- Build may take time on 2GB VPS (swap is already configured)
- If OOM: check \`dmesg | grep -i oom\` from Hetzner console
- SMTP not configured yet — alert delivery will report 'not configured' (expected)" \
  "deploy,size:M"

create_issue \
  "DEPLOY-6: Set up email vendor for alert delivery" \
  "## Summary
Configure an email vendor (Resend or Postmark) so ping can send alert emails when monitors go down.

## Steps
1. Sign up for Resend free tier (3k emails/mo) or Postmark
2. Add and verify sending domain (\`mail.sukhera.dev\` or \`sukhera.dev\`)
3. Configure SPF, DKIM, DMARC records in Cloudflare
4. Add SMTP env vars in Coolify:
   \`\`\`
   SMTP_HOST=smtp.resend.com  (or smtp.postmarkapp.com)
   SMTP_PORT=587
   SMTP_USERNAME=resend (or postmark API token)
   SMTP_PASSWORD=<api-key>
   SMTP_FROM=alerts@sukhera.dev
   \`\`\`
5. Redeploy and test with \`POST /api/v1/alerting/test\`

## AC
- [ ] SPF/DKIM/DMARC records pass verification
- [ ] Test email delivers successfully
- [ ] Alert email arrives when a monitor goes down" \
  "deploy,infra,size:M"

create_issue \
  "DEPLOY-7: Monitor scrt and shrt with ping" \
  "## Summary
Once ping is live, create HTTP monitors for the sibling services so the suite monitors itself.

## Monitors to create
1. **scrt health** — HTTP monitor, \`GET https://scrt.sukhera.dev/health\`, every 60s, 10s grace
2. **shrt health** — HTTP monitor, \`GET https://shrt.sukhera.dev/health\`, every 60s, 10s grace

## Notes
- Per deployment plan: 'Deploy; then point ping at scrt/shrt /health — your suite now monitors itself'
- Later: add UptimeRobot watching only ping (so ping isn't monitoring itself)

## AC
- [ ] Both monitors created and showing 'up' state
- [ ] Dashboard displays status correctly" \
  "deploy,size:S"

echo ""
echo "Done! View issues: gh issue list --label deploy"
