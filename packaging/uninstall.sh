#!/usr/bin/env bash
# Removes the cdx binary and the system plugins installed by install.sh.
set -euo pipefail

prefix="/usr"
bindir=""
plugins_dir=""
destdir="${DESTDIR:-}"

usage() {
	cat <<'EOF'
Usage: ./uninstall.sh [--prefix DIR] [--bindir DIR] [--plugins-dir DIR]

Removes the cdx binary and the default plugins installed by install.sh:
  binary    /usr/bin/cdx
  plugins   /usr/share/cdx/plugins

Options:
  --prefix DIR       installation prefix (default: /usr)
  --bindir DIR       binary directory (default: PREFIX/bin)
  --plugins-dir DIR  plugin directory (default: PREFIX/share/cdx/plugins)
  -h, --help         show this help

DESTDIR removes from a staging root and skips the root check.
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

bindir="${bindir:-$prefix/bin}"
plugins_dir="${plugins_dir:-$prefix/share/cdx/plugins}"

if [ -z "$destdir" ] && [ "$(id -u)" -ne 0 ]; then
	echo "uninstall.sh needs root: run it with sudo" >&2
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

rm -f "$destdir$bindir/cdx"
rm -rf "${destdir}${plugins_dir:?}"
rmdir "$destdir$prefix/share/cdx" 2>/dev/null || true

echo "removed $destdir$bindir/cdx"
echo "removed $destdir$plugins_dir"
echo "user data is kept in ~/.local/share/cdx, ~/.config/cdx, and ~/.local/state/cdx"
