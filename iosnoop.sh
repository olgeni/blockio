#!/bin/sh
#
# iosnoop.sh - run iosnoop.d with disk geometry from diskinfo(8).
#
# The disk layer of the io provider does not carry sector size or media
# size, so the LBA and POS columns come up empty when iosnoop.d runs on
# its own.  This wrapper asks diskinfo(8) about every disk in kern.disks
# and appends the answers to the script as a BEGIN clause.
#
#	./iosnoop.sh				every device
#	./iosnoop.sh ada0			one device
#	./iosnoop.sh ada0,ada1			a list
#	./iosnoop.sh ada0 -c "sleep 10"		trailing dtrace(1) arguments
#
# Needs root, for both diskinfo(8) and dtrace(1).

set -e

dir=$(dirname "$0")
filter=""

case "$1" in
"" | -*) ;;
*)
	filter="$1"
	shift
	;;
esac

script=$(mktemp -t iosnoop)
info=$(mktemp -t diskinfo)
trap 'rm -f "$script" "$info"' EXIT INT TERM

cp "$dir/iosnoop.d" "$script"

for disk in $(sysctl -n kern.disks); do
	diskinfo "/dev/$disk" >> "$info" 2>/dev/null || true
done

printf '%-10s %8s %8s %12s %18s %8s\n' \
    DEVICE SECTOR STRIPE SECTORS MEDIASIZE SIZE
awk '{
	name = $1; sub(".*/", "", name);
	printf "%-10s %8s %8s %12s %18s %7.0fG\n",
	    name, $2, $5, $4, $3, $3 / 1024 / 1024 / 1024;
}' "$info"
echo ""

{
	echo ""
	echo "BEGIN"
	echo "{"
	awk '{
		name = $1; sub(".*/", "", name);
		printf "\tsectorsize[\"%s\"] = %s;\n", name, $2;
		printf "\tmediasize[\"%s\"] = %s;\n", name, $3;
	}' "$info"
	echo "}"
} >> "$script"

exec dtrace -s "$script" "$filter" "$@"
