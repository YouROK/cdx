#!/usr/bin/env bash
# Builds the release artifacts for one Linux architecture: a zip bundle with the
# binary and the default plugins, plus deb, rpm, and apk packages.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/.." && pwd)"

version=""
arch=""
out="${OUT:-dist}"

usage() {
	cat <<'EOF'
Usage: scripts/package.sh --version TAG --arch ARCH [--out DIR]

Builds the release artifacts for one Linux architecture:
  cdx_<version>_linux_<arch>.zip  binary, plugins, install.sh, uninstall.sh
  .deb for Debian and Ubuntu
  .rpm for Fedora, RHEL, and openSUSE
  .apk for Alpine

Architectures: i386, x86_64, armv7, aarch64.
Out directory defaults to dist/. Requires go, zip, and nfpm in PATH.
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--version) version="${2:?--version needs a value}"; shift 2 ;;
	--arch) arch="${2:?--arch needs a value}"; shift 2 ;;
	--out) out="${2:?--out needs a value}"; shift 2 ;;
	-h | --help) usage; exit 0 ;;
	*)
		echo "unknown option: $1" >&2
		usage >&2
		exit 2
		;;
	esac
done

if [ -z "$version" ] || [ -z "$arch" ]; then
	usage >&2
	exit 2
fi

case "$arch" in
i386) goarch="386"; goarm=""; pkgarch="386" ;;
x86_64) goarch="amd64"; goarm=""; pkgarch="amd64" ;;
armv7) goarch="arm"; goarm="7"; pkgarch="arm7" ;;
aarch64) goarch="arm64"; goarm=""; pkgarch="arm64" ;;
*)
	echo "unknown architecture: $arch (expected i386, x86_64, armv7, or aarch64)" >&2
	exit 2
	;;
esac

version="${version#v}"
version="${version#V}"

# Packages need a version without slashes that starts with a digit. Tags that
# already look like a version keep their prerelease and build parts.
if [[ ! "$version" =~ ^[0-9][0-9A-Za-z.+-]*$ ]]; then
	safe="$(printf '%s' "$version" | tr -c '0-9A-Za-z.+.' '.' | tr -s '.' | sed 's/^\.*//; s/\.*$//')"
	[ -n "$safe" ] || safe="0.0.0"
	case "$safe" in
	[0-9]*) version="$safe" ;;
	*) version="0.$safe" ;;
	esac
fi

case "$out" in
"" | "." | ".." | /* | *..*)
	echo "--out must be a relative path inside the repository" >&2
	exit 2
	;;
esac
if [ ! -f "$root_dir/plugins/webui/web/dist/index.html" ]; then
	echo "web assets are missing: run 'npm ci && npm run build' first" >&2
	exit 1
fi
for tool in go zip nfpm; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "$tool is missing from PATH" >&2
		if [ "$tool" = nfpm ]; then
			echo "install it with: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.48.0" >&2
		fi
		exit 1
	fi
done

out_dir="$root_dir/$out"
stage_dir="$out_dir/stage"
commit="$(git -C "$root_dir" rev-parse --short HEAD 2>/dev/null || echo unknown)"
build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

rm -rf "$out_dir"
mkdir -p "$stage_dir/plugins"

echo "building cdx $version for linux/$goarch${goarm:+/armv$goarm}"
(
	cd "$root_dir"
	CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOARM="$goarm" \
		go build -trimpath -buildvcs=false \
			-ldflags "-s -w -X github.com/yourok/cdx/internal/version.Version=$version -X github.com/yourok/cdx/internal/version.Commit=$commit -X github.com/yourok/cdx/internal/version.Date=$build_date" \
			-o "$stage_dir/cdx" ./cmd/cdx
)

# Ship only runtime plugin files: the sources and the Vite setup stay in the repository.
cp -R "$root_dir/plugins/." "$stage_dir/plugins/"
rm -rf "$stage_dir/plugins"/*/web/src "$stage_dir/plugins"/*/web/node_modules
rm -f "$stage_dir/plugins"/*/web/index.html \
	"$stage_dir/plugins"/*/web/vite.config.js \
	"$stage_dir/plugins"/*/web/vite.vendor.config.js \
	"$stage_dir/plugins"/*/web/package.json

# Packages and archives must not inherit the writable bits of the checkout.
find "$stage_dir/plugins" -type d -exec chmod 755 {} +
find "$stage_dir/plugins" -type f -exec chmod 644 {} +
chmod 755 "$stage_dir/cdx"

install -m 755 "$root_dir/packaging/install.sh" "$stage_dir/install.sh"
install -m 755 "$root_dir/packaging/uninstall.sh" "$stage_dir/uninstall.sh"
install -m 644 "$root_dir/README.md" "$stage_dir/README.md"
install -m 644 "$root_dir/LICENSE" "$stage_dir/LICENSE"

zip_name="cdx_${version}_linux_${arch}.zip"
(cd "$stage_dir" && zip -qr "$out_dir/$zip_name" .)

for format in deb rpm apk; do
	STAGE_DIR="$stage_dir" PKG_VERSION="$version" PKG_ARCH="$pkgarch" \
		nfpm package --config "$root_dir/packaging/nfpm.yaml" --packager "$format" --target "$out_dir"
done

rm -rf "$stage_dir"
echo "created:"
find "$out_dir" -maxdepth 1 -type f -printf '  %f\n' | sort
