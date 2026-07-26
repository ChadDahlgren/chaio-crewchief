#!/usr/bin/env sh
#
# Install chaio-crewchief from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/ChadDahlgren/chaio-crewchief/main/scripts/install.sh | sh
#
# macOS users should prefer Homebrew, which handles upgrades:
#   brew install ChadDahlgren/tap/chaio-crewchief
#
# Environment:
#   VERSION   tag to install (default: latest release)
#   BINDIR    install directory (default: /usr/local/bin)
set -eu

REPO="ChadDahlgren/chaio-crewchief"
BIN="chaio-crewchief"
BINDIR="${BINDIR:-/usr/local/bin}"

die() { echo "install: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "requires '$1' on PATH"; }

need uname
need tar
need mktemp

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1" -o "$2"; }
  fetch_stdout() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO "$2" "$1"; }
  fetch_stdout() { wget -qO- "$1"; }
else
  die "requires curl or wget"
fi

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) die "unsupported OS: $os (releases cover linux and darwin)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported architecture: $arch (releases cover amd64 and arm64)" ;;
esac

# Resolve the latest tag from the API unless one was pinned.
version="${VERSION:-}"
if [ -z "$version" ]; then
  version=$(fetch_stdout "https://api.github.com/repos/$REPO/releases/latest" \
    | sed -n 's/.*"tag_name" *: *"\([^"]*\)".*/\1/p' | head -n1)
  [ -n "$version" ] || die "could not determine the latest version; set VERSION=vX.Y.Z"
fi
bare=${version#v}

archive="${BIN}_${bare}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp=$(mktemp -d)
# shellcheck disable=SC2064  # expand $tmp now, not at trap time
trap "rm -rf '$tmp'" EXIT INT TERM

echo "install: fetching $BIN $version ($os/$arch)"
fetch "$base/$archive" "$tmp/$archive" || die "download failed: $base/$archive"

# Verify against the published checksums. A silent no-verify install is how
# people end up running something other than what was released.
if fetch "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
  expected=$(grep " $archive\$" "$tmp/checksums.txt" | awk '{print $1}')
  if [ -n "$expected" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      actual=$(sha256sum "$tmp/$archive" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
      actual=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
    else
      actual=""
      echo "install: WARNING no sha256 tool found, skipping verification" >&2
    fi
    if [ -n "$actual" ] && [ "$actual" != "$expected" ]; then
      die "checksum mismatch for $archive (expected $expected, got $actual)"
    fi
    [ -n "$actual" ] && echo "install: checksum ok"
  else
    echo "install: WARNING $archive not listed in checksums.txt" >&2
  fi
else
  echo "install: WARNING could not fetch checksums.txt, skipping verification" >&2
fi

tar -xzf "$tmp/$archive" -C "$tmp"
[ -f "$tmp/$BIN" ] || die "archive did not contain $BIN"
chmod +x "$tmp/$BIN"

if [ -w "$BINDIR" ]; then
  mv "$tmp/$BIN" "$BINDIR/$BIN"
elif command -v sudo >/dev/null 2>&1; then
  echo "install: $BINDIR is not writable, escalating with sudo"
  sudo mv "$tmp/$BIN" "$BINDIR/$BIN"
else
  die "$BINDIR is not writable and sudo is unavailable; set BINDIR=~/.local/bin"
fi

echo "install: installed to $BINDIR/$BIN"
"$BINDIR/$BIN" version || true
cat <<EOF

Next:
  $BIN doctor     # what's misconfigured?
  $BIN serve      # binds 127.0.0.1:8181

serve has NO authentication and reads provider API keys from its environment.
It binds loopback by default; use --addr :8181 only on a trusted network.
EOF
