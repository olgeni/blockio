/*
 * iosum.d - summarize block I/O by device and command.
 *
 *	dtrace -s iosum.d			until Ctrl-C
 *	dtrace -s iosum.d -c "sleep 5"		for five seconds
 *	dtrace -s iosum.d ada0,ada1		only these devices
 *
 * See iosnoop.d for per-I/O detail.  Names come from devstat at the disk
 * layer and from the GEOM provider otherwise, so a single logical I/O is
 * normally counted once per layer (ada0p4 and ada0).
 */

#pragma D option quiet
#pragma D option defaultargs
#pragma D option dynvarsize=8m
#pragma D option strsize=128

inline string STATNAME = stringof(((struct devstat *)arg1)->device_name);
inline string DEVNAME = STATNAME != "" ?
    strjoin(STATNAME, lltostr(((struct devstat *)arg1)->unit_number)) :
    (((struct bio *)arg0)->bio_to == NULL ? "?" :
     stringof(((struct bio *)arg0)->bio_to->name));

io:::start
/$$1 == "" ||
 strstr(strjoin(",", strjoin($$1, ",")),
        strjoin(",", strjoin(DEVNAME, ","))) != NULL/
{
	@ops[DEVNAME, device_if[((struct devstat *)arg1)->device_type],
	    bio_cmd_string[((struct bio *)arg0)->bio_cmd]] = count();
	@bytes[DEVNAME, device_if[((struct devstat *)arg1)->device_type],
	    bio_cmd_string[((struct bio *)arg0)->bio_cmd]] =
	    sum(((struct bio *)arg0)->bio_length);
	@who[execname] = sum(((struct bio *)arg0)->bio_length);
}

END
{
	printf("%-10s %-8s %-8s %8s %14s\n",
	    "DEVICE", "IF", "CMD", "OPS", "BYTES");
	printa("%-10s %-8s %-8s %@8d %@14d\n", @ops, @bytes);
	printf("\n%-20s %14s\n", "PROCESS", "BYTES");
	printa("%-20s %@14d\n", @who);
}
