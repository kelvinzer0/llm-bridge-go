#!/usr/bin/env bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "Error: This uninstaller must be run as root or via sudo." >&2
  exit 1
fi

BIN_NAME="llm-bridge"
INSTALL_BIN="/usr/local/bin/${BIN_NAME}"
CONFIG_DIR="/etc/${BIN_NAME}"
SYSTEMD_UNIT="/etc/systemd/system/${BIN_NAME}.service"
INITD_SCRIPT="/etc/init.d/${BIN_NAME}"

echo "Stopping service..."
if command -v systemctl >/dev/null 2>&1 && [ -f "${SYSTEMD_UNIT}" ]; then
  systemctl stop "${BIN_NAME}.service" || true
  systemctl disable "${BIN_NAME}.service" || true
  rm -f "${SYSTEMD_UNIT}"
  systemctl daemon-reload
elif [ -f "${INITD_SCRIPT}" ]; then
  "${INITD_SCRIPT}" stop || true
  rm -f "${INITD_SCRIPT}"
fi

echo "Removing binary and files..."
rm -f "${INSTALL_BIN}"
rm -rf "${CONFIG_DIR}"

echo "Uninstallation complete."
