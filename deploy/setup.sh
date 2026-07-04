#!/bin/bash
set -euo pipefail

# new-meridian node setup script
# Usage: ./setup.sh <API_KEY> <EGRESS_PROXY>
# Example: ./setup.sh abc123def "socks5://user:pass@host:port"

API_KEY="${1:?Usage: ./setup.sh <API_KEY> <EGRESS_PROXY>}"
EGRESS_PROXY="${2:?Usage: ./setup.sh <API_KEY> <EGRESS_PROXY>}"
REPO="https://github.com/qwwqq1000-arch/new-meridian.git"
INSTALL_DIR="/root/new-meridian"
BUN_VERSION="1.3.14"
GO_VERSION="1.24.4"

echo "=== Installing dependencies ==="

# bun
if ! command -v /root/.bun/bin/bun &>/dev/null; then
  curl -fsSL https://bun.sh/install | BUN_INSTALL=/root/.bun bash -s "bun-v${BUN_VERSION}"
fi
echo "bun: $(/root/.bun/bin/bun --version)"

# go (for building native-egress)
if ! command -v /usr/local/go/bin/go &>/dev/null; then
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xzf -
fi
export PATH=$PATH:/usr/local/go/bin
echo "go: $(go version)"

echo "=== Cloning repo ==="
if [ -d "$INSTALL_DIR/.git" ]; then
  cd "$INSTALL_DIR" && git pull origin main
else
  git clone "$REPO" "$INSTALL_DIR"
  cd "$INSTALL_DIR"
fi

echo "=== Installing node modules ==="
/root/.bun/bin/bun install --frozen-lockfile 2>/dev/null || /root/.bun/bin/bun install

echo "=== Building native-egress ==="
cd "$INSTALL_DIR/native-egress"
go build -o native-egress .
echo "NE built: $(ls -la native-egress)"

echo "=== Writing config ==="

# .env (per-node API key)
sed "s/__API_KEY__/${API_KEY}/" "$INSTALL_DIR/deploy/env.template" > "$INSTALL_DIR/.env"
echo ".env written"

# settings.json (per-node egress proxy)
mkdir -p /root/.config/meridian
ESCAPED_PROXY=$(printf '%s\n' "$EGRESS_PROXY" | sed 's/[&/\]/\\&/g')
sed "s|__EGRESS_PROXY__|${ESCAPED_PROXY}|" "$INSTALL_DIR/deploy/settings.json" > /root/.config/meridian/settings.json
echo "settings.json written"

# startup scripts
cp "$INSTALL_DIR/deploy/start_meridian.sh" "$INSTALL_DIR/start_meridian.sh"
cp "$INSTALL_DIR/deploy/start_ne.sh" "$INSTALL_DIR/start_ne.sh"
chmod +x "$INSTALL_DIR/start_meridian.sh" "$INSTALL_DIR/start_ne.sh"

echo "=== Starting services ==="
bash "$INSTALL_DIR/start_ne.sh"
sleep 2
curl -s http://127.0.0.1:9999/health && echo " NE OK" || echo " NE FAILED"

bash "$INSTALL_DIR/start_meridian.sh"
sleep 3
curl -s http://127.0.0.1:3456/health | python3 -c "import sys,json;d=json.load(sys.stdin);print('Meridian', d['status'])" 2>/dev/null || echo "Meridian FAILED"

echo ""
echo "=== Setup complete ==="
echo "Meridian: http://0.0.0.0:3456"
echo "NE:       http://127.0.0.1:9999"
echo ""
echo "To check: curl http://localhost:3456/health"
echo "Logs:     /tmp/meridian.log, /tmp/ne_live.log"
