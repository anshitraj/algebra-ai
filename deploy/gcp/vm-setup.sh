#!/bin/bash
# Sets up the Algebra VM (Debian 12): Postgres 16, Redis, the Cloudflare tunnel client, the service
# units, nightly backups, automatic security updates and the Google Ops Agent for logs. Run as root
# on the VM from this folder (deploy.sh uploads it and runs it); safe to run again.
#
# Needs /etc/algebra/deploy.conf: PROJECT, KMS_KEY, BACKUP_BUCKET (deploy.sh writes it).
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
HERE=$(cd "$(dirname "$0")" && pwd)

install -d -m 0755 /etc/algebra
[ -f /etc/algebra/deploy.conf ] || { echo "missing /etc/algebra/deploy.conf (PROJECT, KMS_KEY, BACKUP_BUCKET)" >&2; exit 1; }
# shellcheck disable=SC1091
. /etc/algebra/deploy.conf

MD=http://metadata.google.internal/computeMetadata/v1
secret() {
  curl -fsS -H "Authorization: Bearer $TOKEN" \
    "https://secretmanager.googleapis.com/v1/projects/$PROJECT/secrets/$1/versions/latest:access" | jq -er .payload.data | base64 -d | tr -d '\r\n'
}

echo "== packages"
apt-get update -y
apt-get install -y --no-install-recommends ca-certificates curl gnupg jq lsb-release unattended-upgrades
TOKEN=$(curl -fsS -H 'Metadata-Flavor: Google' "$MD/instance/service-accounts/default/token" | jq -r .access_token)

# Postgres 16 from the project's own repository, the version the tests run against.
install -d -m 0755 /usr/share/postgresql-common/pgdg
curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc
echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt $(lsb_release -cs)-pgdg main" > /etc/apt/sources.list.d/pgdg.list
# Cloudflare's own repository, for the tunnel client.
install -d -m 0755 /usr/share/keyrings
curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg -o /usr/share/keyrings/cloudflare-main.gpg
echo 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main' > /etc/apt/sources.list.d/cloudflared.list
apt-get update -y
apt-get install -y postgresql-16 redis-server cloudflared
# Debian ships a mail server listening on localhost:25; Algebra sends mail through Resend, so remove it.
apt-get purge -y exim4-base exim4-config exim4-daemon-light >/dev/null 2>&1 || true

echo "== users and folders"
id algebra >/dev/null 2>&1 || useradd --system --home /opt/algebra --shell /usr/sbin/nologin algebra
id cloudflared >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin cloudflared
install -d -m 0755 /opt/algebra /opt/algebra/bin /opt/algebra/backend
install -d -o algebra -g algebra -m 0750 /var/lib/algebra
install -d -o root -g algebra -m 0750 /etc/algebra

echo "== postgres"
dbpw=$(secret algebra-db-password)
runuser -u postgres -- psql -v ON_ERROR_STOP=1 -q <<'SQL'
SELECT 'CREATE ROLE algebra LOGIN' WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'algebra') \gexec
SELECT 'CREATE DATABASE algebra OWNER algebra' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'algebra') \gexec
SQL
# The password goes in on stdin (never on a command line anyone can list with ps). psql's own variables
# (\set) were tried first and set a different password; this is the form that was tested.
printf "ALTER ROLE algebra PASSWORD '%s';\n" "$dbpw" | runuser -u postgres -- psql -v ON_ERROR_STOP=1 -q
unset dbpw
# Sized for 16 GB of memory and an SSD; a settings file of its own, so a package upgrade never overwrites it.
install -d /etc/postgresql/16/main/conf.d
cat > /etc/postgresql/16/main/conf.d/algebra.conf <<'CONF'
shared_buffers = 2GB
effective_cache_size = 8GB
maintenance_work_mem = 512MB
work_mem = 16MB
random_page_cost = 1.1
wal_compression = on
checkpoint_completion_target = 0.9
log_min_duration_statement = 1000
CONF
systemctl restart postgresql
# Postgres listens on localhost only and has TLS on (APP_ENV=production refuses a database without it).
echo "listen_addresses=$(runuser -u postgres -- psql -Atc 'show listen_addresses') ssl=$(runuser -u postgres -- psql -Atc 'show ssl')"

echo "== redis"
rpw=$(secret algebra-redis-password)
conf=/etc/redis/redis.conf
sed -i '/^# BEGIN algebra$/,/^# END algebra$/d' "$conf"
cat >> "$conf" <<EOF
# BEGIN algebra
bind 127.0.0.1 -::1
protected-mode yes
requirepass $rpw
maxmemory 512mb
maxmemory-policy volatile-lru
# END algebra
EOF
unset rpw
systemctl restart redis-server

echo "== services"
install -m 0750 "$HERE/vm/algebra-fetch-secrets" /usr/local/sbin/algebra-fetch-secrets
install -m 0755 "$HERE/vm/algebra-api-start" /usr/local/sbin/algebra-api-start
install -m 0750 "$HERE/vm/algebra-backup" /usr/local/sbin/algebra-backup
install -m 0640 -o root -g algebra "$HERE/vm/api.conf" /etc/algebra/api.conf
install -m 0644 "$HERE"/vm/*.service "$HERE"/vm/*.timer /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now algebra-backup.timer
systemctl enable algebra-demo-provider.service algebra-api.service cloudflared.service

echo "== automatic security updates (reboot at 03:00 India time when one needs it)"
cat > /etc/apt/apt.conf.d/52algebra-unattended <<'EOF'
Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-Time "21:30";
Unattended-Upgrade::Remove-Unused-Dependencies "true";
EOF
systemctl enable --now unattended-upgrades

echo "== Google Ops Agent (logs and metrics to Cloud Logging and Monitoring)"
if ! dpkg -s google-cloud-ops-agent >/dev/null 2>&1; then
  curl -fsSL https://dl.google.com/cloudagents/add-google-cloud-ops-agent-repo.sh -o /tmp/add-ops-agent-repo.sh
  bash /tmp/add-ops-agent-repo.sh --also-install
  rm -f /tmp/add-ops-agent-repo.sh
fi
echo "== done"
