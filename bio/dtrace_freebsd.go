package bio

import (
	"fmt"
	"strings"
)

// script is the D program.  The device table is generated: diskinfo(8)
// knows the media size, the io provider's disk layer does not.
const script = `
#pragma D option quiet
#pragma D option bufsize=8m
#pragma D option aggsize=16m
#pragma D option dynvarsize=8m
#pragma D option switchrate=%dms
#pragma D option aggrate=%dms

inline int BUCKETS = %d;

int64_t media[string];		/* int would truncate at 4G */
int track[string];

BEGIN
{
%s}

/*
 * Only the disk layer: it names the device through devstat, while the
 * GEOM layer above it reports the same I/O again under the provider name.
 */
io:::start
/track[strjoin(stringof(((struct devstat *)arg1)->device_name),
               lltostr(((struct devstat *)arg1)->unit_number))] != 0/
{
	this->dev = strjoin(stringof(((struct devstat *)arg1)->device_name),
	    lltostr(((struct devstat *)arg1)->unit_number));
	this->cmd = ((struct bio *)arg0)->bio_cmd;
	this->len = ((struct bio *)arg0)->bio_length;
	this->bucket = (((struct bio *)arg0)->bio_offset * BUCKETS) / media[this->dev];

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
		fmt.Fprintf(&table, "\tmedia[\"%s\"] = %d;\n", d.Name, d.MediaSize)
		fmt.Fprintf(&table, "\ttrack[\"%s\"] = 1;\n", d.Name)
	}
	half := intervalMS / 2
	if half < 10 {
		half = 10
	}
	return fmt.Sprintf(script, half, half, buckets, table.String(), intervalMS)
}
