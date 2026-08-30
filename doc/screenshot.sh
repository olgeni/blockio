#!/bin/sh
#
# Regenerate doc/main.png.  Run it while the disks are busy: a scrub, a
# resilver, or a few dd(1) readers scattered over the devices.  freeze(1)
# comes from sysutils/freeze.
#
# The background is set to the map's idle color because freeze draws each
# run of background color half a character short, which otherwise leaves
# thin dark slivers where the color changes.

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
	--background "#202020"
