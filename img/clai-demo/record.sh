#!/usr/bin/env bash
# Records img/clai-demo.gif. Run from the repository root:
#   bash img/clai-demo/record.sh
# VHS renders a GIF through palettegen, which keeps one colour histogram per
# captured frame: 1400 frames at 10 fps needed 3.2 GB and the kernel killed
# ffmpeg. So vhs records an MP4 (streaming x264) and this script encodes the GIF
# from a palette taken every third second. (2026-10-24 demo recording)
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

# Keep the PNG frame sequence off /tmp: it is a tmpfs on many hosts.
frames=$(mktemp -d "${XDG_CACHE_HOME:-$HOME/.cache}/clai-demo-frames.XXXXXX")
trap 'rm -rf "$frames"' EXIT
TMPDIR="$frames" vhs img/clai-demo.tape

ffmpeg -y -loglevel error -i img/clai-demo.mp4 \
	-vf 'fps=1/3,palettegen=max_colors=256' /tmp/clai-demo-palette.png

ffmpeg -y -loglevel error -i img/clai-demo.mp4 -i /tmp/clai-demo-palette.png \
	-filter_complex '[0:v]fps=10[x];[x][1:v]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle' \
	-loop 0 img/clai-demo.gif

rm -f img/clai-demo.mp4 /tmp/clai-demo-palette.png
