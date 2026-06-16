#!/bin/bash
# mini-control worker setup
# sudo bash -c "$(curl -fsSL https://<server>/install.sh)"
set -euo pipefail

CONFIG_DIR="/etc/minicontrol"
CONFIG_PATH="${CONFIG_DIR}/config.yaml"
TART_INSTALL_DIR="/usr/local/lib/tart"
BASE_IMAGE_REGISTRY="ghcr.io/cirruslabs/macos-tahoe-xcode:26.3"
LOCAL_IMAGE_NAME="minictl-tahoe-base"
ENROLL_KEY_FILENAME="mini-control-enroll.key"

die() {
	echo "FATAL: $*" >&2
	exit 1
}

cleanup_files=()
cleanup() {
	for path in "${cleanup_files[@]}"; do
		rm -f "$path"
	done
}
trap cleanup EXIT

# parse a JSON string value by key (no external deps)
json_val() {
	osascript -l JavaScript \
		-e 'function run(argv) {
			var data = $.NSFileHandle.fileHandleWithStandardInput.readDataToEndOfFile;
			var str = $.NSString.alloc.initWithDataEncoding(data, 4).js;
			return JSON.parse(str)[argv[0]];
		}' -- "$2" <<<"$1"
}
yaml_quote() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/''/g")"; }
read_yaml_single_quoted_value() {
	local key="$1"
	local file="$2"
	[[ -r "$file" ]] || return 0
	sed -n "s/^${key}: *'\\(.*\\)'/\\1/p" "$file" | head -n 1 | sed "s/''/'/g"
}
prompt_secret() {
	local prompt="$1"
	local value=""
	IFS= read -r -s -p "$prompt" value </dev/tty || die "failed to read secret from tty"
	printf '\n' >&2
	printf '%s' "$value"
}
load_worker_token_from_file() {
	local key_path="$1"
	local value=""
	[[ -r "$key_path" ]] || return 1
	IFS= read -r value <"$key_path" || true
	value="$(printf '%s' "$value" | tr -d '[:space:]')"
	[[ -n "$value" ]] || return 1
	WORKER_TOKEN="$value"
	echo "using enrollment token from $key_path"
}
resolve_worker_token() {
	if [[ -n "$WORKER_TOKEN" ]]; then
		return 0
	fi
	load_worker_token_from_file "${REAL_HOME}/${ENROLL_KEY_FILENAME}"
}

# --- defaults (server replaces only the address when served via /install.sh) ---

SERVER_ADDR="__SERVER_ADDR__"
WORKER_TOKEN=""
HOST_PASSWORD=""
HOST_PASSWORD_FILE=""
PROMPT_HOST_PASSWORD=false
SKIP_IMAGE_PULL=false
NO_REBOOT=false
FORCED_HOSTNAME=""

while [[ $# -gt 0 ]]; do
	case $1 in
	--server)
		SERVER_ADDR="$2"
		shift 2
		;;
	--token)
		WORKER_TOKEN="$2"
		shift 2
		;;
	--host-password-file)
		HOST_PASSWORD_FILE="$2"
		shift 2
		;;
	--prompt-host-password)
		PROMPT_HOST_PASSWORD=true
		shift
		;;
	--skip-image)
		SKIP_IMAGE_PULL=true
		shift
		;;
	--no-reboot)
		NO_REBOOT=true
		shift
		;;
	--hostname)
		FORCED_HOSTNAME="$2"
		shift 2
		;;
	--help | -h)
		echo "Usage: curl -fsSL https://<server>/install.sh | sudo bash"
		echo "  first install: place the enrollment token in ~/mini-control-enroll.key"
		echo "  or:  sudo bash setup-host.sh --server <addr> --token <token> [--prompt-host-password] [--skip-image] [--no-reboot] [--hostname <name>]"
		exit 0
		;;
	*) die "Unknown option: $1" ;;
	esac
done

[[ "$SERVER_ADDR" == "__SERVER_ADDR__" ]] && die "--server is required (or fetch this script from the server)"

# --- preflight ---

[[ "$(uname)" != "Darwin" ]] && die "macOS only"
[[ "$(uname -m)" != "arm64" ]] && die "Apple Silicon (arm64) required"
[[ "$(id -u)" -ne 0 ]] && die "run with sudo"

DISK_FREE_GB=$(df -g / | tail -1 | awk '{print $4}')
[[ "$DISK_FREE_GB" -lt 60 ]] && die "need 60GB free (have ${DISK_FREE_GB}GB)"

REAL_USER="${SUDO_USER:-$(logname 2>/dev/null || echo "$USER")}"
REAL_HOME=$(eval echo "~$REAL_USER")
resolve_worker_token || true
SERVER_PORT="443"
if [[ "$SERVER_ADDR" == \[*\]:* ]]; then
	SERVER_HOST="${SERVER_ADDR%%]*}"
	SERVER_HOST="${SERVER_HOST#[}"
	SERVER_PORT="${SERVER_ADDR##*:}"
elif [[ "$SERVER_ADDR" == *:* ]] && [[ "$SERVER_ADDR" != *:*:* ]]; then
	SERVER_HOST="${SERVER_ADDR%%:*}"
	SERVER_PORT="${SERVER_ADDR##*:}"
else
	SERVER_HOST="$SERVER_ADDR"
fi

EXISTING_HOST_PASSWORD="$(read_yaml_single_quoted_value "host_keychain_password" "$CONFIG_PATH")"
if [[ -n "$HOST_PASSWORD_FILE" ]]; then
	[[ -r "$HOST_PASSWORD_FILE" ]] || die "host password file is not readable: $HOST_PASSWORD_FILE"
	IFS= read -r HOST_PASSWORD <"$HOST_PASSWORD_FILE" || true
elif [[ -z "$HOST_PASSWORD" && -n "$EXISTING_HOST_PASSWORD" ]]; then
	HOST_PASSWORD="$EXISTING_HOST_PASSWORD"
	echo "preserving existing host keychain password from $CONFIG_PATH"
elif [[ "$PROMPT_HOST_PASSWORD" == "true" ]] || [[ -z "$HOST_PASSWORD" ]]; then
	HOST_PASSWORD="$(prompt_secret "Host login password: ")"
fi

curl_server() {
	curl --fail --show-error --silent --location "$@"
}

enable_screen_sharing() {
	local kickstart="/System/Library/CoreServices/RemoteManagement/ARDAgent.app/Contents/Resources/kickstart"
	if [[ ! -x "$kickstart" ]]; then
		echo "screen sharing setup skipped: kickstart not found"
		return
	fi

	echo "enabling screen sharing for ${REAL_USER}..."
	"$kickstart" -activate -configure -access -on -users "$REAL_USER" -privs -all -restart -agent -menu || true
	launchctl enable system/com.apple.screensharing 2>/dev/null || true
	launchctl kickstart -k system/com.apple.screensharing 2>/dev/null || true
	echo "  screen-sharing=on user=${REAL_USER}"
}

run_tart_as_user() {
	sudo -u "$REAL_USER" env PATH="/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin" "$TART_LINK" "$@"
}

worker_token_valid() {
	local token="$1"
	[[ -z "$token" ]] && return 1

	local response
	response=$(curl_server -sf --max-time 10 \
		-H "Authorization: Bearer ${token}" \
		"https://${SERVER_ADDR}/api/v1/proxy/sessions/pending" 2>/dev/null || true)

	[[ "$response" == "[]" ]] || [[ "$response" == \[* ]]
}

# --- grab server TLS fingerprint ---

SERVER_CERT_FILE=$(mktemp /tmp/minicontrol-server-cert.XXXXXX.pem)
cleanup_files+=("$SERVER_CERT_FILE")

</dev/null openssl s_client -showcerts -connect "${SERVER_HOST}:${SERVER_PORT}" -servername "$SERVER_HOST" 2>/dev/null |
	awk '
		BEGIN { capture = 0 }
		/-----BEGIN CERTIFICATE-----/ { capture = 1 }
		capture { print }
		/-----END CERTIFICATE-----/ { exit }
	' >"$SERVER_CERT_FILE" || true
[[ -s "$SERVER_CERT_FILE" ]] || die "failed to fetch server certificate"

TLS_FINGERPRINT=$(
	openssl x509 -in "$SERVER_CERT_FILE" -outform DER 2>/dev/null |
		shasum -a 256 | awk '{print $1}'
) || true
[[ -z "$TLS_FINGERPRINT" ]] && die "failed to read server certificate fingerprint"
echo "server tls fingerprint: $TLS_FINGERPRINT"

echo "preflight ok: macOS $(sw_vers -productVersion), arm64, ${DISK_FREE_GB}GB free, user=$REAL_USER"

# --- tart (from GitHub releases) ---

TART_BIN="$TART_INSTALL_DIR/tart.app/Contents/MacOS/tart"
TART_LINK="/usr/local/bin/tart"

install_tart() {
	echo "fetching latest tart release..."
	local release_json
	release_json=$(curl -sf --max-time 15 \
		"https://api.github.com/repos/cirruslabs/tart/releases/latest") ||
		die "failed to query tart releases"

	local tag
	tag=$(json_val "$release_json" "tag_name")
	echo "installing tart $tag..."

	local tmp_tar="/tmp/tart-install-$$.tar.gz"
	curl -fSL --max-time 120 \
		"https://github.com/cirruslabs/tart/releases/download/${tag}/tart.tar.gz" \
		-o "$tmp_tar"

	rm -rf "$TART_INSTALL_DIR"
	mkdir -p "$TART_INSTALL_DIR"
	tar -xzf "$tmp_tar" -C "$TART_INSTALL_DIR"
	rm -f "$tmp_tar"

	# symlink into PATH
	mkdir -p "$(dirname "$TART_LINK")"
	ln -sf "$TART_BIN" "$TART_LINK"

	echo "installed tart $tag"
}

if ! command -v tart &>/dev/null; then
	install_tart
else
	echo "tart already installed: $(tart --version 2>/dev/null || echo 'unknown')"
fi

# --- worker binary ---

echo "fetching latest release from server..."
LATEST_JSON=$(curl_server -sf --max-time 10 "https://${SERVER_ADDR}/api/v1/releases/latest") ||
	die "no release on server — upload one first with scripts/build-and-publish.sh"

LATEST_VERSION=$(json_val "$LATEST_JSON" "version")
LATEST_SHA256=$(json_val "$LATEST_JSON" "sha256")

echo "downloading v${LATEST_VERSION}..."
WORKER_TMP="/tmp/minicontrol-worker-install-$$"
cleanup_files+=("$WORKER_TMP")
curl_server -fSL --max-time 300 \
	"https://${SERVER_ADDR}/api/v1/releases/download/${LATEST_VERSION}" \
	-o "$WORKER_TMP"
chmod 700 "$WORKER_TMP"

ACTUAL_SHA256=$(shasum -a 256 "$WORKER_TMP" | cut -d' ' -f1)
[[ "$ACTUAL_SHA256" != "$LATEST_SHA256" ]] &&
	die "sha256 mismatch: expected $LATEST_SHA256, got $ACTUAL_SHA256"

codesign --verify --deep --strict "$WORKER_TMP" 2>/dev/null ||
	die "codesign rejected binary — must be Developer ID signed"

xattr -cr "$WORKER_TMP" 2>/dev/null || true

install -m 755 "$WORKER_TMP" /usr/local/bin/minicontrol-worker
chown "$REAL_USER" /usr/local/bin/minicontrol-worker
echo "installed minicontrol-worker v${LATEST_VERSION}"

# --- base image ---

if [[ "$SKIP_IMAGE_PULL" == "false" ]]; then
	if ! run_tart_as_user list 2>/dev/null | grep -q "$LOCAL_IMAGE_NAME"; then
		echo "pulling base image (slow)..."
		run_tart_as_user pull "$BASE_IMAGE_REGISTRY"
		run_tart_as_user clone "$BASE_IMAGE_REGISTRY" "$LOCAL_IMAGE_NAME" 2>/dev/null || true
	fi
fi

# --- config ---

mkdir -p "$CONFIG_DIR"
EXISTING_WORKER_TOKEN=""
EXISTING_WORKER_TOKEN="$(read_yaml_single_quoted_value "worker_token" "$CONFIG_PATH")"

if worker_token_valid "$EXISTING_WORKER_TOKEN"; then
	echo "preserving existing worker_token (validated)"
	WRITE_WORKER_TOKEN="$EXISTING_WORKER_TOKEN"
	WRITE_ENROLLMENT_TOKEN=""
else
	[[ -n "$WORKER_TOKEN" ]] ||
		die "enrollment token is required for first install or re-enrollment; put it in ${REAL_HOME}/${ENROLL_KEY_FILENAME} or pass --token"
	if [[ -n "$EXISTING_WORKER_TOKEN" ]]; then
		echo "existing worker_token invalid, re-enrolling with enrollment token"
	fi
	WRITE_WORKER_TOKEN=""
	WRITE_ENROLLMENT_TOKEN="$WORKER_TOKEN"
fi

cat >"$CONFIG_DIR/config.yaml" <<YAML
server_address: $(yaml_quote "$SERVER_HOST")
worker_token: $(yaml_quote "$WRITE_WORKER_TOKEN")
enrollment_token: $(yaml_quote "$WRITE_ENROLLMENT_TOKEN")
base_image: $(yaml_quote "$LOCAL_IMAGE_NAME")
pool_size: 2
ssh_username: 'admin'
ssh_password: 'admin'
http_port: ${SERVER_PORT}
use_https: true
tls_cert_fingerprint: $(yaml_quote "$TLS_FINGERPRINT")
log_level: 'info'
YAML

if [[ -n "$HOST_PASSWORD" ]]; then
	echo "host_keychain_password: $(yaml_quote "$HOST_PASSWORD")" >>"$CONFIG_DIR/config.yaml"
fi
chown -R "$REAL_USER" "$CONFIG_DIR"
chmod 600 "$CONFIG_DIR/config.yaml"

# --- system tuning ---

echo "applying power management..."
pmset -a sleep 0 displaysleep 0 disksleep 0
pmset -a womp 1
pmset -a powernap 0
pmset -a autorestart 1
echo "  sleep=off displaysleep=off disksleep=off womp=on powernap=off autorestart=on"

echo "disabling macOS auto-update..."
defaults write /Library/Preferences/com.apple.SoftwareUpdate AutomaticDownload -bool false
defaults write /Library/Preferences/com.apple.SoftwareUpdate AutomaticallyInstallMacOSUpdates -bool false

# systemsetup outputs Error:-99 on Sequoia but still applies (exit 0)
echo "configuring systemsetup..."
systemsetup -setrestartpowerfailure on 2>/dev/null || true
systemsetup -setrestartfreeze on 2>/dev/null || true
systemsetup -setremotelogin on 2>/dev/null || true
echo "  restart-on-power-failure=on restart-on-freeze=on remote-login=on"

enable_screen_sharing

# local network (macOS 15+)
defaults write com.apple.network.local-network AllowedEthernetLocalNetworkAddresses \
	-array "10.0.0.0/8" "172.16.0.0/12" "192.168.0.0/16"
defaults write com.apple.network.local-network AllowedWiFiLocalNetworkAddresses \
	-array "10.0.0.0/8" "172.16.0.0/12" "192.168.0.0/16"

# DHCP lease
defaults write /Library/Preferences/SystemConfiguration/com.apple.InternetSharing.default.plist \
	bootpd -dict DHCPLeaseTimeSecs -int 60
rm -f /var/db/dhcpd_leases

# --- hostname (idempotent: skip if already enrolled) ---

CURRENT_HOSTNAME=$(scutil --get LocalHostName 2>/dev/null || echo "")
if [[ -n "$FORCED_HOSTNAME" ]]; then
	echo "hostname -> $FORCED_HOSTNAME (forced)"
	scutil --set HostName "$FORCED_HOSTNAME"
	scutil --set LocalHostName "$FORCED_HOSTNAME"
	scutil --set ComputerName "$FORCED_HOSTNAME"
	systemsetup -setcomputername "$FORCED_HOSTNAME" 2>/dev/null || true
	dscacheutil -flushcache
elif [[ -n "$EXISTING_WORKER_TOKEN" && -n "$CURRENT_HOSTNAME" ]]; then
	echo "preserving existing hostname: $CURRENT_HOSTNAME"
else
	ASSIGNED_NAME=""
	HARDWARE_UUID=$(
		ioreg -rd1 -c IOPlatformExpertDevice 2>/dev/null |
			awk -F'"' '/IOPlatformUUID/{print $(NF-1); exit}'
	)
	if [[ -n "$WORKER_TOKEN" && -n "$HARDWARE_UUID" ]]; then
		ASSIGN_JSON=$(curl_server -sf -X POST --max-time 10 \
			-H "Authorization: Bearer ${WORKER_TOKEN}" \
			-H "Content-Type: application/json" \
			-d "{\"hardware_uuid\":\"${HARDWARE_UUID}\"}" \
			"https://${SERVER_ADDR}/api/v1/hostname/assign" 2>/dev/null || true)
		if [[ -n "$ASSIGN_JSON" ]]; then
			ASSIGNED_NAME=$(json_val "$ASSIGN_JSON" "hostname" 2>/dev/null || true)
		fi
	fi

	if [[ -n "$ASSIGNED_NAME" ]]; then
		echo "hostname -> $ASSIGNED_NAME"
		scutil --set HostName "$ASSIGNED_NAME"
		scutil --set LocalHostName "$ASSIGNED_NAME"
		scutil --set ComputerName "$ASSIGNED_NAME"
		systemsetup -setcomputername "$ASSIGNED_NAME" 2>/dev/null || true
		dscacheutil -flushcache
	fi
fi

# --- launchd ---

BACKEND_PLIST="/Library/LaunchDaemons/com.minicontrol.worker.plist"
OVERLAY_LOGINWINDOW_PLIST="/Library/LaunchAgents/com.minicontrol.overlay.plist"
LEGACY_OVERLAY_BOOTSTRAP_PLIST="/Library/LaunchDaemons/com.minicontrol.overlay-bootstrap.plist"
LEGACY_OVERLAY_GUI_PLIST="${CONFIG_DIR}/com.minicontrol.overlay.plist"
OVERLAY_STATE_DIR="/Library/Application Support/minicontrol"
OVERLAY_STATE_PATH="${OVERLAY_STATE_DIR}/overlay-state.json"

launchctl bootout system "$BACKEND_PLIST" 2>/dev/null || true
launchctl bootout system "$LEGACY_OVERLAY_BOOTSTRAP_PLIST" 2>/dev/null || true
launchctl bootout system "$OVERLAY_LOGINWINDOW_PLIST" 2>/dev/null || true
rm -f "$LEGACY_OVERLAY_BOOTSTRAP_PLIST" "$LEGACY_OVERLAY_GUI_PLIST"

# Create runtime/log directories
mkdir -p "${REAL_HOME}/Library/Logs"
mkdir -p "/Library/Logs"
mkdir -p "${OVERLAY_STATE_DIR}"
chown "$REAL_USER" "${REAL_HOME}/Library/Logs"
chown -R "$REAL_USER":wheel "${OVERLAY_STATE_DIR}"
chmod 755 "${OVERLAY_STATE_DIR}"

cat >"$BACKEND_PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.minicontrol.worker</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/minicontrol-worker</string>
        <string>--config</string>
        <string>${CONFIG_DIR}/config.yaml</string>
        <string>--overlay-state-file</string>
        <string>${OVERLAY_STATE_PATH}</string>
    </array>
    <key>UserName</key>
    <string>${REAL_USER}</string>
    <key>SessionCreate</key>
    <true/>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>StandardOutPath</key>
    <string>${REAL_HOME}/Library/Logs/minicontrol-worker.log</string>
    <key>StandardErrorPath</key>
    <string>${REAL_HOME}/Library/Logs/minicontrol-worker.err</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
        <key>HOME</key>
        <string>${REAL_HOME}</string>
    </dict>
    <key>ThrottleInterval</key>
    <integer>10</integer>
</dict>
</plist>
PLIST

cat >"$OVERLAY_LOGINWINDOW_PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.minicontrol.overlay</string>
    <key>ThrottleInterval</key>
    <integer>30</integer>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/minicontrol-worker</string>
        <string>--config</string>
        <string>${CONFIG_DIR}/config.yaml</string>
        <string>--overlay-client</string>
        <string>--overlay-state-file</string>
        <string>${OVERLAY_STATE_PATH}</string>
    </array>
    <key>OnDemand</key>
    <false/>
    <key>LimitLoadToSessionType</key>
    <string>LoginWindow</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
    </dict>
    <key>ProcessType</key>
    <string>Interactive</string>
    <key>StandardOutPath</key>
    <string>/Library/Logs/minicontrol-overlay.log</string>
    <key>StandardErrorPath</key>
    <string>/Library/Logs/minicontrol-overlay.err</string>
</dict>
</plist>
PLIST

launchctl bootstrap system "$BACKEND_PLIST"

# --- done ---

echo ""
echo "tart:   $(tart --version 2>/dev/null || echo 'installed')"
echo "worker: $(minicontrol-worker --version 2>/dev/null || echo 'installed')"
echo "config: $CONFIG_DIR/config.yaml"
echo "enroll: ${REAL_HOME}/${ENROLL_KEY_FILENAME}"
echo "logs:   ${REAL_HOME}/Library/Logs/minicontrol-worker.log"
echo "overlay: log show --predicate 'process == \"minicontrol-worker\"'"
echo "state:  ${OVERLAY_STATE_PATH}"
echo ""
if [[ "$NO_REBOOT" == "true" ]]; then
	echo "setup complete. reboot manually to apply system changes."
	exit 0
fi

echo "rebooting in 5s to apply system changes..."
sleep 5
shutdown -r now
