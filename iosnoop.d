/*
 * iosnoop.d - trace individual block I/O operations.
 *
 *	dtrace -s iosnoop.d			every device
 *	dtrace -s iosnoop.d ada0		one device
 *	dtrace -s iosnoop.d ada0,ada1,ada0p4	a list (exact names, no spaces)
 *
 * Prints one line per completed I/O, then per-device totals and a latency
 * histogram on exit.
 *
 * Each logical I/O is usually seen twice: once at the GEOM layer, named
 * after the provider (ada0p4, gpt/foo, zvol/...), and once at the disk
 * layer (ada0).  Filter by name to keep just the layer you care about.
 *
 * LBA and POS need the device geometry.  GEOM providers carry it on the
 * bio; the disk layer does not, so run iosnoop.sh instead of this script
 * directly to have the diskinfo(8) numbers filled in.
 */

#pragma D option quiet
#pragma D option defaultargs
#pragma D option switchrate=10hz
#pragma D option dynvarsize=8m
#pragma D option strsize=128

/* geometry table, filled in by iosnoop.sh from diskinfo(8); empty otherwise */
int64_t sectorsize[string];	/* int would truncate mediasize at 4G */
int64_t mediasize[string];

/*
 * The disk layer fills in devstat; the GEOM layer leaves it empty and
 * carries the name on the bio's target provider instead.
 */
inline string STATNAME = stringof(((struct devstat *)arg1)->device_name);
inline string DEVNAME = STATNAME != "" ?
    strjoin(STATNAME, lltostr(((struct devstat *)arg1)->unit_number)) :
    (((struct bio *)arg0)->bio_to == NULL ? "?" :
     stringof(((struct bio *)arg0)->bio_to->name));

BEGIN
{
	printf("%-12s %-10s %-6s %10s %12s %7s %8s\n",
	    "EXEC", "DEV", "CMD", "BYTES", "LBA", "POS", "us");
}

io:::start
/$$1 == "" ||
 strstr(strjoin(",", strjoin($$1, ",")),
        strjoin(",", strjoin(DEVNAME, ","))) != NULL/
{
	ts[arg0] = timestamp;
	sz[arg0] = ((struct bio *)arg0)->bio_length;
	off[arg0] = ((struct bio *)arg0)->bio_offset;
	cmd[arg0] = ((struct bio *)arg0)->bio_cmd;
	who[arg0] = execname;
	dev[arg0] = DEVNAME;

	/*
	 * Geometry, taken from the same layer the name came from: a disk
	 * layer bio can point at a much smaller provider, and mixing the
	 * two turns POS into nonsense.
	 */
	ssz[arg0] = STATNAME != "" ? sectorsize[DEVNAME] :
	    ((struct bio *)arg0)->bio_to->sectorsize;
	msz[arg0] = STATNAME != "" ? mediasize[DEVNAME] :
	    ((struct bio *)arg0)->bio_to->mediasize;
}

io:::done
/ts[arg0]/
{
	this->us = (timestamp - ts[arg0]) / 1000;
	this->lba = off[arg0] / (ssz[arg0] > 0 ? ssz[arg0] : 512);
	/* position within the device, in tenths of a percent */
	this->pos = msz[arg0] > 0 ? (off[arg0] * 1000) / msz[arg0] : -1;
	this->pstr = this->pos < 0 ? "-" :
	    strjoin(strjoin(lltostr(this->pos / 10), "."),
	            strjoin(lltostr(this->pos % 10), "%"));

	printf("%-12s %-10s %-6s %10d %12d %7s %8d\n",
	    who[arg0], dev[arg0], bio_cmd_string[cmd[arg0]],
	    sz[arg0], this->lba, this->pstr, this->us);

	@bytes[dev[arg0], bio_cmd_string[cmd[arg0]]] = sum(sz[arg0]);
	@ops[dev[arg0], bio_cmd_string[cmd[arg0]]] = count();
	@lat[dev[arg0], bio_cmd_string[cmd[arg0]]] = quantize(this->us);

	ts[arg0] = 0; sz[arg0] = 0; off[arg0] = 0; cmd[arg0] = 0;
	who[arg0] = 0; dev[arg0] = 0; ssz[arg0] = 0; msz[arg0] = 0;
}

END
{
	printf("\n%-10s %-8s %8s %14s\n", "DEVICE", "CMD", "OPS", "BYTES");
	printa("%-10s %-8s %@8d %@14d\n", @ops, @bytes);
	printf("\nlatency (us):\n");
	printa(@lat);
}
