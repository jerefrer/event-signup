#!/usr/bin/env bash
set -euo pipefail

HOST="${1:?Usage: ./deploy.sh hostname}"
REMOTE_DIR="/home/deployer/event-signup"

echo "Building..."
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC="zig cc -target x86_64-linux-musl" CXX="zig c++ -target x86_64-linux-musl" go build -ldflags="-s -w" -o event-signup-app

echo "Ensuring remote directory exists..."
ssh "root@$HOST" bash -s <<SETUP
set -euo pipefail
id deployer &>/dev/null || useradd -m -s /bin/bash deployer
mkdir -p $REMOTE_DIR
chown deployer:deployer $REMOTE_DIR
SETUP

echo "Uploading files..."
scp event-signup-app "deployer@$HOST:$REMOTE_DIR/event-signup-app.new"
scp .env "deployer@$HOST:$REMOTE_DIR/.env"
scp event-signup.service "root@$HOST:/etc/systemd/system/event-signup.service"
scp nginx.conf "root@$HOST:/etc/nginx/sites-available/event-signup"

echo "Deploying..."
ssh "root@$HOST" bash -s <<'EOF'
set -euo pipefail

# Nginx
ln -sf /etc/nginx/sites-available/event-signup /etc/nginx/sites-enabled/event-signup
nginx -t
nginx -s reload

# App
chown deployer:deployer /home/deployer/event-signup/.env
chmod 600 /home/deployer/event-signup/.env
systemctl stop event-signup || true
cd /home/deployer/event-signup
mv event-signup-app.new event-signup-app
chmod +x event-signup-app
systemctl daemon-reload
systemctl enable event-signup
systemctl start event-signup
echo "Deployed. Status:"
systemctl status event-signup --no-pager
EOF
