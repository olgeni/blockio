package bio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// script is the D program.  macOS carries the Solaris io provider, so it
// looks nothing like the FreeBSD one: args[0] is a bufinfo_t translated
// from struct buf, with the byte count in b_bcount, the direction in
// b_flags and the block number in b_blkno.
//
// Devices have to be keyed by minor number.  The devinfo_t translator in
// /usr/lib/dtrace/io.d leaves dev_name and dev_statname as the literal
// "??", so there is no device name to be had; the minor is what /dev/diskN
// carries, and every volume of a disk (diskNsM) reports blocks in the whole
// disk's address space, so they all map to the same slot.
//
// There is no TRIM here either: unmap does not travel through the buffer
// cache, so the io provider never sees it.
const script = `
#pragma D option quiet
#pragma D option bufsize=8m
#pragma D option aggsize=16m
#pragma D option dynvarsize=8m
#pragma D option switchrate=%dms
#pragma D option aggrate=%dms

inline int BUCKETS = %d;

int64_t media[int];	/* bytes, by minor */
int64_t bsize[int];	/* block size, by minor */
string name[int];	/* the whole disk this minor belongs to */
int track[int];

BEGIN
{
%s}

io:::start
/track[args[1]->dev_minor] != 0/
{
	this->m = args[1]->dev_minor;
	this->dev = name[this->m];
	this->cmd = (args[0]->b_flags & B_READ) != 0 ? %d : %d;
	this->len = (int64_t)args[0]->b_bcount;
	this->off = (int64_t)args[0]->b_blkno * bsize[this->m];
	this->bucket = (this->off * BUCKETS) / media[this->m];

	@cells[this->dev, this->cmd, this->bucket] = sum(this->len);
	@ops[this->dev, this->cmd] = count();
	@bytes[this->dev, this->cmd] = sum(this->len);
}

tick-%dms
{
	printa("C %%s %%d %%d %%@d\n", @cells);
	printa("S %%s %%d %%@d %%@d\n", @ops, @bytes);
	printf("T\n");

	trunc(@cells);
	trunc(@ops);
	trunc(@bytes);
}
`

// Script returns the D program that traces the given disks.
func Script(disks []Disk, intervalMS, buckets int) string {
	var table strings.Builder
	for _, d := range disks {
		for _, minor := range minors(d.Name) {
			fmt.Fprintf(&table, "\tmedia[%d] = %d;\n", minor, d.MediaSize)
			fmt.Fprintf(&table, "\tbsize[%d] = %d;\n", minor, d.SectorSize)
			fmt.Fprintf(&table, "\tname[%d] = \"%s\";\n", minor, d.Name)
			fmt.Fprintf(&table, "\ttrack[%d] = 1;\n", minor)
		}
	}
	return buildScript(table.String(), intervalMS, buckets)
}

func buildScript(table string, intervalMS, buckets int) string {
	half := intervalMS / 2
	if half < 10 {
		half = 10
	}
	return fmt.Sprintf(script, half, half, buckets, table,
		int(CmdRead), int(CmdWrite), intervalMS)
}

// minors lists the device minor numbers a whole disk answers to: its own,
// and one per slice of it (disk3, disk3s1, disk3s1s1, ...).
func minors(name string) []int {
	paths, _ := filepath.Glob("/dev/" + name + "s*")
	paths = append([]string{"/dev/" + name}, paths...)

	seen := map[int]bool{}
	var out []int
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		sys, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		minor := int(sys.Rdev & 0xffffff)
		if !seen[minor] {
			seen[minor] = true
			out = append(out, minor)
		}
	}
	return out
}
