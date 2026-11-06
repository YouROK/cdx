#!/usr/bin/env bash
# Installs the cdx binary and the bundled default plugins.
set -euo pipefail

prefix="/usr"
bindir=""
plugins_dir=""
destdir="${DESTDIR:-}"

usage() {
	cat <<'EOF'
Usage: ./install.sh [--prefix DIR] [--bindir DIR] [--plugins-dir DIR]

Installs cdx and the default plugins from this archive:
  binary    /usr/bin/cdx
  plugins   /usr/share/cdx/plugins

Options:
  --prefix DIR       installation prefix (default: /usr)
  --bindir DIR       binary directory (default: PREFIX/bin)
  --plugins-dir DIR  plugin directory (default: PREFIX/share/cdx/plugins)
  -h, --help         show this help

DESTDIR installs into a staging root and skips the root check.
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--prefix) prefix="${2:?--prefix needs a value}"; shift 2 ;;
	--bindir) bindir="${2:?--bindir needs a value}"; shift 2 ;;
	--plugins-dir) plugins_dir="${2:?--plugins-dir needs a value}"; shift 2 ;;
	-h | --help) usage; exit 0 ;;
	*)
		echo "unknown option: $1" >&2
		usage >&2
		exit 2
		;;
	esac
done

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
bindir="${bindir:-$prefix/bin}"
plugins_dir="${plugins_dir:-$prefix/share/cdx/plugins}"

if [ -z "$destdir" ] && [ "$(id -u)" -ne 0 ]; then
	echo "install.sh needs root: run it with sudo" >&2
	exit 1
fi
case "$plugins_dir" in
/*) ;;
*)
	echo "the plugin directory must be an absolute path: $plugins_dir" >&2
	exit 2
	;;
esac
case "$bindir" in
/*) ;;
*)
	echo "the binary directory must be an absolute path: $bindir" >&2
	exit 2
	;;
esac
depth="${plugins_dir//[!\/]/}"
if [ "${#depth}" -lt 3 ]; then
	echo "refusing to use '$plugins_dir' as the plugin directory: pass a nested absolute path" >&2
	exit 2
fi
if [ ! -f "$script_dir/cdx" ]; then
	echo "archive is incomplete: cdx is missing next to install.sh" >&2
	exit 1
fi
if [ ! -d "$script_dir/plugins" ]; then
	echo "archive is incomplete: plugins/ is missing next to install.sh" >&2
	exit 1
fi

install -d -m 755 "$destdir$bindir"
install -m 755 "$script_dir/cdx" "$destdir$bindir/cdx"

rm -rf "${destdir}${plugins_dir:?}"
install -d -m 755 "$destdir$plugins_dir"
cp -R "$script_dir/plugins/." "$destdir$plugins_dir/"

echo "installed $destdir$bindir/cdx"
echo "installed plugins to $destdir$plugins_dir"
if [ "$plugins_dir" != "/usr/share/cdx/plugins" ]; then
	echo "note: cdx reads system plugins from /usr/share/cdx/plugins by default"
	echo "set CDX_SYSTEM_PLUGIN_DIR=$plugins_dir to use this location"
fi
