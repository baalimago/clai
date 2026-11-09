#!/bin/sh
set -eu

# Stands in for ffmpeg over a swapped PATH. Discovery and recording are told
# apart by the arguments, as the real program does. Every mode is chosen with
# FAKE_FFMPEG_*_MODE; defaults produce a machine with one microphone and one
# monitor.

# write_wav writes a 16 kHz mono s16 WAV with two samples: two above the
# silence threshold for "loud", two zeros for "silent". The header sizes are
# 36 + 4 bytes of samples.
write_wav() {
  out="$1"
  level="${2:-loud}"
  printf 'RIFF\050\000\000\000WAVEfmt \020\000\000\000\001\000\001\000\200\076\000\000\000\175\000\000\002\000\020\000data\004\000\000\000' >"$out"
  if [ "$level" = "silent" ]; then
    printf '\000\000\000\000' >>"$out"
  else
    printf '\020\047\020\047' >>"$out"
  fi
}

# le32 prints a number as four little-endian bytes.
le32() {
  n="$1"
  printf "\\$(printf '%03o' $((n & 255)))\\$(printf '%03o' $((n >> 8 & 255)))\\$(printf '%03o' $((n >> 16 & 255)))\\$(printf '%03o' $((n >> 24 & 255)))"
}

# open_wav writes the header ffmpeg writes before the first sample: both sizes
# are the 0xFFFFFFFF placeholder until the file is finalized (measured
# 2026-11-10 with ffmpeg 8.0.1).
open_wav() {
  printf 'RIFF\377\377\377\377WAVEfmt \020\000\000\000\001\000\001\000\200\076\000\000\000\175\000\000\002\000\020\000data\377\377\377\377' >"$1"
}

# close_wav finalizes the header in place, as ffmpeg does on its one SIGINT.
close_wav() {
  size=$(($(wc -c <"$1")))
  le32 $((size - 8)) | dd of="$1" bs=1 seek=4 conv=notrunc 2>/dev/null
  le32 $((size - 44)) | dd of="$1" bs=1 seek=40 conv=notrunc 2>/dev/null
}

# grow_wav appends two loud samples every 20 ms until the process ends.
grow_wav() {
  while :; do
    printf '\020\047\020\047' >>"$1"
    sleep 0.02
  done
}

pulse_list() {
  kind="$1"
  if [ "$kind" = "sources" ]; then
    mode="${FAKE_FFMPEG_SOURCES_MODE:-ok}"
  else
    mode="${FAKE_FFMPEG_SINKS_MODE:-ok}"
  fi
  if [ "$mode" = "slow" ]; then
    sleep 1
  fi
  case "$kind:$mode" in
    sources:nolibpulse | sinks:nolibpulse)
      echo "some unrelated output"
      ;;
    sources:refused)
      echo "Auto-detected sources for pulse:"
      echo "Cannot list sources: Connection refused"
      ;;
    sinks:refused)
      echo "Auto-detected sinks for pulse:"
      echo "Cannot list sinks: Connection refused"
      ;;
    sinks:empty)
      echo "Auto-detected sinks for pulse:"
      ;;
    sources:miconly)
      cat <<'EOF'
Auto-detected sources for pulse:
* alsa_input.pci-0000_00_1f.3.analog-stereo [Built-in Audio Analog Stereo] (none)
EOF
      ;;
    sources:ok | sources:slow)
      cat <<'EOF'
Auto-detected sources for pulse:
  alsa_output.pci-0000_00_1f.3.analog-stereo.monitor [Monitor of Built-in Audio Analog Stereo] (none)
* alsa_input.pci-0000_00_1f.3.analog-stereo [Built-in Audio Analog Stereo] (none)
EOF
      ;;
    sources:twosinks)
      cat <<'EOF'
Auto-detected sources for pulse:
  alsa_output.pci-0000_00_1f.3.analog-stereo.monitor [Monitor of Built-in Audio Analog Stereo] (none)
  alsa_output.usb-headset.analog-stereo.monitor [Monitor of USB Headset] (none)
* alsa_input.pci-0000_00_1f.3.analog-stereo [Built-in Audio Analog Stereo] (none)
EOF
      ;;
    sinks:ok | sinks:slow)
      cat <<'EOF'
Auto-detected sinks for pulse:
* alsa_output.pci-0000_00_1f.3.analog-stereo [Built-in Audio Analog Stereo] (none)
EOF
      ;;
    sinks:two)
      cat <<'EOF'
Auto-detected sinks for pulse:
* alsa_output.pci-0000_00_1f.3.analog-stereo [Built-in Audio Analog Stereo] (none)
  alsa_output.usb-headset.analog-stereo [USB Headset] (none)
EOF
      ;;
  esac
}

case "$*" in
  *"-sources pulse"*)
    pulse_list sources
    exit 0
    ;;
  *"-sinks pulse"*)
    pulse_list sinks
    exit 0
    ;;
  *"avfoundation -list_devices"*)
    if [ "${FAKE_FFMPEG_AVFOUNDATION_MODE:-}" = "slow" ]; then
      sleep 1
    fi
    case "${FAKE_FFMPEG_AVFOUNDATION_MODE:-ok}" in
      ok | slow)
        cat <<'EOF'
AVFoundation video devices:
[0] FaceTime HD Camera
AVFoundation audio devices:
[0] Built-in Microphone
[1] BlackHole 2ch
EOF
        ;;
      noloopback)
        cat <<'EOF'
AVFoundation audio devices:
[0] Built-in Microphone
EOF
        ;;
      unsupported)
        echo "Unknown input format: 'avfoundation'"
        ;;
    esac
    exit 0
    ;;
esac

if [ -n "${FAKE_FFMPEG_ARGS_LOG:-}" ]; then
  printf '%s\n' "$*" >>"$FAKE_FFMPEG_ARGS_LOG"
fi

case "$*" in
  *"-f null -"*)
    # An ambient measurement: astats dumps one Peak_level and RMS_level line
    # per reset to stdout, about 20 frames per second on the real program.
    case "${FAKE_FFMPEG_MEASURE_MODE:-quiet}" in
      quiet)
        cat <<'EOF'
frame:0    pts:0       pts_time:0
lavfi.astats.1.Peak_level=-33.1
lavfi.astats.1.RMS_level=-45.6
lavfi.astats.Overall.Peak_level=-31.9
lavfi.astats.Overall.RMS_level=-44.2
frame:1    pts:801     pts_time:0.0500208
lavfi.astats.1.Peak_level=-30.4
lavfi.astats.1.RMS_level=-44.9
lavfi.astats.Overall.Peak_level=-29.8
lavfi.astats.Overall.RMS_level=-43.7
frame:2    pts:1602    pts_time:0.100042
lavfi.astats.1.Peak_level=-36.6
lavfi.astats.1.RMS_level=-46.1
lavfi.astats.Overall.Peak_level=-35.2
lavfi.astats.Overall.RMS_level=-45.1
EOF
        ;;
      silence)
        cat <<'EOF'
frame:0    pts:0       pts_time:0
lavfi.astats.Overall.Peak_level=-inf
lavfi.astats.Overall.RMS_level=-inf
EOF
        ;;
      loud)
        printf '%s\n' 'lavfi.astats.Overall.RMS_level=-0.4'
        ;;
      none) ;;
      fail)
        echo "ffmpeg: device open failed" >&2
        exit 1
        ;;
      *)
        echo "unknown FAKE_FFMPEG_MEASURE_MODE=$FAKE_FFMPEG_MEASURE_MODE" >&2
        exit 2
        ;;
    esac
    exit 0
    ;;
esac

out=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "wav" ]; then
    out="$arg"
  fi
  prev="$arg"
done
if [ -z "$out" ]; then
  echo "fake ffmpeg: missing output wav path" >&2
  exit 2
fi

trap 'exit 255' INT TERM

case "${FAKE_FFMPEG_RECORD_MODE:-spoken}" in
  spoken)
    write_wav "$out"
    echo "[silencedetect @ 0x1] silence_start: -0.000916667" >&2
    echo "[silencedetect @ 0x1] silence_end: 0.100000 | silence_duration: 0.100000" >&2
    echo "[silencedetect @ 0x1] silence_start: 0.200000" >&2
    while :; do sleep 0.1; done
    ;;
  silent)
    write_wav "$out" silent
    echo "[silencedetect @ 0x1] silence_start: -0.000916667" >&2
    while :; do sleep 0.1; done
    ;;
  continuous)
    # A source that never pauses emits no silencedetect event at all.
    write_wav "$out"
    while :; do sleep 0.1; done
    ;;
  growing)
    # The real recorder: the header holds placeholders while samples arrive,
    # and only an interrupt finalizes it. A kill leaves the placeholders.
    open_wav "$out"
    trap 'sleep "${FAKE_FFMPEG_FINALIZE_SECONDS:-0.05}"; close_wav "$out"; exit 255' INT TERM
    grow_wav "$out"
    ;;
  stubborn)
    # A recorder that ignores the graceful stop: only a kill ends it.
    open_wav "$out"
    trap '' INT TERM
    grow_wav "$out"
    ;;
  stubborn_empty)
    # Ignores the graceful stop before it wrote a single byte.
    : >"$out"
    trap '' INT TERM
    while :; do sleep 0.02; done
    ;;
  fail)
    write_wav "$out"
    echo "ffmpeg: device open failed" >&2
    exit 1
    ;;
  ends)
    write_wav "$out"
    echo "ffmpeg: ended early" >&2
    exit 3
    ;;
  *)
    echo "unknown FAKE_FFMPEG_RECORD_MODE=$FAKE_FFMPEG_RECORD_MODE" >&2
    exit 2
    ;;
esac