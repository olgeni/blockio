#!/bin/sh
#
# Regenerate doc/main.png.  Run it while the disks are busy: a scrub, a
# resilver, or a few dd(1) readers scattered over the devices.  freeze(1)
# comes from sysutils/freeze.
#
# The background is set to black, the color of an idle cell, because freeze
# draws each run of background color half a character short, which otherwise
# leaves thin slivers where the color changes.
#
# pngquant takes the result from about 1.3MB to 100KB; the map is a handful
# of colors on black, so the palette costs nothing visible.

set -e
cd "$(dirname "$0")/.."

sudo ./blockio -a -once -for 5s -size 130x32 -color truecolor -buckets 8192 > doc/main.ansi

freeze doc/main.ansi -o doc/main.png \
	--window \
	--padding 24 \
	--margin 30 \
	--font.size 12 \
	--line-height 1.15 \
	--border.radius 8 \
	--shadow.blur 24 \
	--shadow.y 12 \
	--background "#000000"

pngquant --quality 70-95 --speed 1 --force --output doc/main.png -- doc/main.png
