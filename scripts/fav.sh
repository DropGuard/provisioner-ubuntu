#!/usr/bin/env bash
# fav.sh — copy the given image into ~/Pictures/Favorites.
#
# If a file with the same name already exists in the target, do nothing
# (no overwrite, no error). Designed to be bound to a hotkey in an image
# viewer that can pass the current file path (qimgv: %file%).
#
# Usage:
#   fav.sh /path/to/image.jpg
#
# Exit codes:
#   0  copied (or already present)
#   1  no argument / source missing

set -euo pipefail

if [[ $# -lt 1 ]]; then
    echo "usage: fav.sh <image-path>" >&2
    exit 1
fi

src="$1"
if [[ ! -f "${src}" ]]; then
    echo "fav.sh: not a file: ${src}" >&2
    exit 1
fi

dest_dir="${FAV_DIR:-$HOME/Pictures/Favorites}"
mkdir -p "${dest_dir}"

base="$(basename "${src}")"
dest="${dest_dir}/${base}"

if [[ -e "${dest}" ]]; then
    # Already favorited — silently skip.
    notify-send "Already in favorites" "${base}" 2>/dev/null || true
    exit 0
fi

cp "${src}" "${dest}"
notify-send "Favorited" "${base} → ${dest_dir}" 2>/dev/null || true
exit 0
