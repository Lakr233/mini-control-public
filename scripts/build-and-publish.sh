#!/bin/bash
# Build, sign, notarize, and publish the minicontrol-worker binary.
# Usage: ./scripts/build-and-publish.sh --server <url> --token <token> --version <ver>
set -euo pipefail

# ─── Signing Identity ───
IDENTITY="${CODE_SIGN_IDENTITY:-}"
KEYCHAIN_PROFILE="${NOTARY_KEYCHAIN_PROFILE:-}"

# ─── Argument Parsing ───
SERVER=""
TOKEN=""
VERSION=""
SKIP_NOTARIZE=false
CA_CERT=""
INSECURE=false

while [[ $# -gt 0 ]]; do
	case $1 in
	--server)
		SERVER="$2"
		shift 2
		;;
	--token)
		TOKEN="$2"
		shift 2
		;;
	--version)
		VERSION="$2"
		shift 2
		;;
	--cacert)
		CA_CERT="$2"
		shift 2
		;;
	--insecure)
		INSECURE=true
		shift
		;;
	--skip-notarize)
		SKIP_NOTARIZE=true
		shift
		;;
	--help | -h)
		echo "Usage: $0 --server <url> --token <token> --version <ver> [--cacert <pem>] [--insecure] [--skip-notarize]"
		exit 0
		;;
	*)
		echo "Unknown option: $1"
		exit 1
		;;
	esac
done

[[ -z "$SERVER" ]] && echo "Error: --server is required" && exit 1
[[ -z "$TOKEN" ]] && echo "Error: --token is required" && exit 1
[[ -z "$VERSION" ]] && echo "Error: --version is required" && exit 1
[[ -z "$IDENTITY" ]] && echo "Error: CODE_SIGN_IDENTITY is required" && exit 1
if [[ "$SKIP_NOTARIZE" == "false" && -z "$KEYCHAIN_PROFILE" ]]; then
	echo "Error: NOTARY_KEYCHAIN_PROFILE is required unless --skip-notarize is set"
	exit 1
fi

# ─── Resolve Paths ───
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
WORKER_DIR="$PROJECT_ROOT/worker"

# ─── Inject Version ───
MAIN_SWIFT="$WORKER_DIR/Sources/MiniControlWorker/main.swift"
sed -i '' "s/currentVersion = \".*\"/currentVersion = \"$VERSION\"/" "$MAIN_SWIFT"
trap 'cd "$PROJECT_ROOT" && git checkout -- "$MAIN_SWIFT"' EXIT

echo "==> Building worker binary (release) v${VERSION}..."
cd "$WORKER_DIR"
swift build -c release 2>&1 | tail -5
BIN_DIR="$(swift build -c release --show-bin-path)"
BINARY="$BIN_DIR/MiniControlWorker"

if [[ ! -f "$BINARY" ]]; then
	echo "Error: Binary not found at $BINARY"
	exit 1
fi

echo "==> Binary built: $BINARY ($(du -h "$BINARY" | cut -f1))"

# ─── Code Signing ───
echo "==> Signing with: $IDENTITY"
codesign --sign "$IDENTITY" \
	--options runtime \
	--timestamp \
	--force \
	"$BINARY"

echo "==> Verifying signature..."
codesign --verify --verbose=2 "$BINARY"

# ─── Notarization ───
if [[ "$SKIP_NOTARIZE" == "false" ]]; then
	echo "==> Submitting for notarization..."
	# Create a zip for notarization (notarytool requires zip/dmg/pkg)
	NOTARIZE_ZIP="/tmp/minicontrol-worker-${VERSION}-notarize.zip"
	ditto -c -k --keepParent "$BINARY" "$NOTARIZE_ZIP"

	xcrun notarytool submit "$NOTARIZE_ZIP" \
		--keychain-profile "$KEYCHAIN_PROFILE" \
		--wait

	rm -f "$NOTARIZE_ZIP"
	echo "==> Notarization complete"
else
	echo "==> Skipping notarization (--skip-notarize)"
fi

# ─── Final Verification ───
echo "==> Final spctl check..."
spctl --assess --type execute "$BINARY" && echo "spctl: PASS" || echo "spctl: WARN (may need notarization)"

# ─── Compute SHA256 ───
SHA256=$(shasum -a 256 "$BINARY" | cut -d' ' -f1)
SIZE=$(stat -f%z "$BINARY")
echo "==> SHA256: $SHA256"
echo "==> Size: $SIZE bytes"

# ─── Upload to Server ───
echo "==> Uploading to $SERVER..."
curl_args=(-s -w "\n%{http_code}")
if [[ -n "$CA_CERT" ]]; then
	curl_args+=(--cacert "$CA_CERT")
fi
if [[ "$INSECURE" == "true" ]]; then
	curl_args+=(-k)
fi
RESPONSE=$(curl "${curl_args[@]}" \
	--http1.1 \
	-X PUT \
	-H "Content-Type: application/octet-stream" \
	-H "Expect:" \
	-H "Authorization: Bearer $TOKEN" \
	--data-binary "@$BINARY" \
	"$SERVER/api/v1/releases/upload/$VERSION")

HTTP_CODE=$(echo "$RESPONSE" | tail -1)
BODY=$(echo "$RESPONSE" | sed '$d')

if [[ "$HTTP_CODE" == "201" ]]; then
	echo "==> Upload successful!"
	echo "$BODY"
elif [[ "$HTTP_CODE" == "409" ]]; then
	echo "Error: Upload failed with HTTP 409"
	echo "$BODY"
	exit 1
else
	echo "Error: Upload failed with HTTP $HTTP_CODE"
	echo "$BODY"
	exit 1
fi

echo ""
echo "==> Release v${VERSION} published successfully"
echo "    SHA256: $SHA256"
echo "    Size:   $SIZE bytes"
echo "    Server: $SERVER"
