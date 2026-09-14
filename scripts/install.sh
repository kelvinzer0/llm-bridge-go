#!/usr/bin/env bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "Error: This installer must be run as root or via sudo." >&2
  exit 1
fi

BIN_NAME="llm-bridge"
INSTALL_BIN="/usr/local/bin/${BIN_NAME}"
CONFIG_DIR="/etc/${BIN_NAME}"
CONFIG_FILE="${CONFIG_DIR}/${BIN_NAME}.env"
SYSTEMD_UNIT="/etc/systemd/system/${BIN_NAME}.service"
INITD_SCRIPT="/etc/init.d/${BIN_NAME}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "${SCRIPT_DIR}")"

echo "=== Installing ${BIN_NAME} ==="

# 1. Locate or build binary
if [ -f "${ROOT_DIR}/bin/${BIN_NAME}" ]; then
  cp "${ROOT_DIR}/bin/${BIN_NAME}" "${INSTALL_BIN}"
elif [ -f "${ROOT_DIR}/${BIN_NAME}" ]; then
  cp "${ROOT_DIR}/${BIN_NAME}" "${INSTALL_BIN}"
else
  echo "Compiling ${BIN_NAME}..."
  (cd "${ROOT_DIR}" && go build -trimpath -ldflags="-s -w" -o "${INSTALL_BIN}" ./cmd/llm-bridge)
fi

chmod +x "${INSTALL_BIN}"
echo "Installed executable to ${INSTALL_BIN}"

# 2. Setup configuration directory
mkdir -p "${CONFIG_DIR}"
if [ ! -f "${CONFIG_FILE}" ]; then
  cat << 'EOF' > "${CONFIG_FILE}"
# Strict localhost binding (do not set to 0.0.0.0)
HOST=127.0.0.1
PORT=8080
EOF
  chmod 600 "${CONFIG_FILE}"
  echo "Created configuration at ${CONFIG_FILE}"
else
  echo "Configuration file already exists at ${CONFIG_FILE}, keeping intact."
fi

# 3. Setup service based on init system
if command -v systemctl >/dev/null 2>&1 && [ -d /etc/systemd/system ]; then
  echo "Configuring systemd service..."
  cp "${SCRIPT_DIR}/${BIN_NAME}.service" "${SYSTEMD_UNIT}"
  systemctl daemon-reload
  systemctl enable "${BIN_NAME}.service"
  systemctl restart "${BIN_NAME}.service"
  echo "Systemd service enabled and started."
  echo "Usage:"
  echo "  service ${BIN_NAME} start"
  echo "  service ${BIN_NAME} stop"
  echo "  service ${BIN_NAME} restart"
  echo "  service ${BIN_NAME} status"
else
  echo "Configuring SysVinit fallback service in ${INITD_SCRIPT}..."
  cat << EOF > "${INITD_SCRIPT}"
#!/bin/sh
### BEGIN INIT INFO
# Provides:          ${BIN_NAME}
# Required-Start:    \$network \$remote_fs \$local_fs
# Required-Stop:     \$network \$remote_fs \$local_fs
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: ${BIN_NAME} daemon
### END INIT INFO

PIDFILE="/var/run/${BIN_NAME}.pid"
CONFIG="${CONFIG_FILE}"
DAEMON="${INSTALL_BIN}"

[ -f "\$CONFIG" ] && . "\$CONFIG"
HOST="\${HOST:-127.0.0.1}"
PORT="\${PORT:-8080}"

case "\$1" in
  start)
    echo "Starting ${BIN_NAME} on \$HOST:\$PORT..."
    start-stop-daemon --start --background --make-pidfile --pidfile "\$PIDFILE" \\
      --exec "\$DAEMON" -- -host "\$HOST" -port "\$PORT"
    ;;
  stop)
    echo "Stopping ${BIN_NAME}..."
    start-stop-daemon --stop --pidfile "\$PIDFILE" --retry 5
    rm -f "\$PIDFILE"
    ;;
  restart)
    \$0 stop
    sleep 1
    \$0 start
    ;;
  status)
    start-stop-daemon --status --pidfile "\$PIDFILE"
    case "\$?" in
      0) echo "${BIN_NAME} is running." ;;
      1) echo "${BIN_NAME} is not running and the pid file exists." ;;
      3) echo "${BIN_NAME} is not running." ;;
      4) echo "Unable to determine ${BIN_NAME} status." ;;
    esac
    ;;
  *)
    echo "Usage: \$0 {start|stop|restart|status}"
    exit 1
    ;;
esac
exit 0
EOF
  chmod +x "${INITD_SCRIPT}"
  if command -v update-rc.d >/dev/null 2>&1; then
    update-rc.d "${BIN_NAME}" defaults
  fi
  "${INITD_SCRIPT}" restart
fi

echo "=== ${BIN_NAME} installation completed successfully ==="
