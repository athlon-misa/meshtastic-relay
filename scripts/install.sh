#!/usr/bin/env bash
set -euo pipefail

REPO="athlon-misa/meshtastic-relay"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
CONFIG_DIR="${CONFIG_DIR:-/etc/relayd}"

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  aarch64|arm64) SUFFIX="linux-arm64" ;;
  armv7l)        SUFFIX="linux-armv7" ;;
  x86_64)        SUFFIX="linux-amd64" ;;
  *)             echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Get latest release tag (or use specified version)
VERSION="${1:-latest}"
if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
fi

echo "Installing relayd ${VERSION} (${SUFFIX})..."

# Download binary
URL="https://github.com/${REPO}/releases/download/${VERSION}/relayd-${SUFFIX}"
curl -fsSL -o /tmp/relayd "$URL"
chmod +x /tmp/relayd
sudo mv /tmp/relayd "${INSTALL_DIR}/relayd"

# Set up config directory and example config if not present
if [ ! -d "$CONFIG_DIR" ]; then
  sudo mkdir -p "$CONFIG_DIR"
  curl -fsSL "https://raw.githubusercontent.com/${REPO}/${VERSION}/config.example.yaml" \
    | sudo tee "${CONFIG_DIR}/config.yaml" > /dev/null
  echo "Example config written to ${CONFIG_DIR}/config.yaml — edit it before starting."
fi

# Install systemd service if not present
if [ ! -f /etc/systemd/system/relayd.service ]; then
  sudo tee /etc/systemd/system/relayd.service > /dev/null <<'SERVICE'
[Unit]
Description=Meshtastic Relay Daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/relayd -config /etc/relayd/config.yaml
Restart=on-failure
RestartSec=5
DynamicUser=yes
SupplementaryGroups=dialout
StateDirectory=relayd
ReadWritePaths=/etc/relayd

[Install]
WantedBy=multi-user.target
SERVICE
  sudo systemctl daemon-reload
  echo "Systemd service installed. Enable with: sudo systemctl enable --now relayd"
fi

echo "Done. relayd ${VERSION} installed to ${INSTALL_DIR}/relayd"
