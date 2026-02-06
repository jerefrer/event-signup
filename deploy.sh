#!/usr/bin/env bash
set -euo pipefail

HOST="${1:?Usage: ./deploy.sh hostname}"
REMOTE_DIR="/home/deploy/event-signup"

echo "Building..."
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o event-signup-app

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
chown deploy:deploy /home/deploy/event-signup/.env
chmod 600 /home/deploy/event-signup/.env
systemctl stop event-signup || true
cd /home/deploy/event-signup
mv event-signup-app.new event-signup-app
chmod +x event-signup-app
systemctl daemon-reload
systemctl enable event-signup
systemctl start event-signup
echo "Deployed. Status:"
systemctl status event-signup --no-pager
EOF
