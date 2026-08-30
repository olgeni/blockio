// Package bio discovers block devices and traces their I/O with dtrace(1).
package bio

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Disk is one block device, as diskinfo(8) describes it.
type Disk struct {
	Name       string // "ada0"
	SectorSize int64
	MediaSize  int64
	Sectors    int64
	Stripe     int64
	Descr      string // "Samsung SSD 870 EVO 1TB"
	Rotation   int    // 0 for solid state, -1 when unknown
}

// Size renders the media size the way diskinfo(8) does.
func (d Disk) Size() string { return HumanBytes(d.MediaSize) }

// Disks lists every disk in kern.disks that answers diskinfo(8).  Devices
// without media (an empty cd0, say) are left out.
func Disks() ([]Disk, error) {
	out, err := exec.Command("sysctl", "-n", "kern.disks").Output()
	if err != nil {
		return nil, fmt.Errorf("sysctl kern.disks: %w", err)
	}

	var disks []Disk
	for _, name := range strings.Fields(string(out)) {
		d, err := Info(name)
		if err != nil {
			continue // no media, or not readable
		}
		disks = append(disks, d)
	}
	sort.Slice(disks, func(i, j int) bool { return lessDevice(disks[i].Name, disks[j].Name) })
	return disks, nil
}

// Info asks diskinfo(8) about one device.
func Info(name string) (Disk, error) {
	d := Disk{Name: name, Rotation: -1}

	out, err := exec.Command("diskinfo", "-v", "/dev/"+name).Output()
	if err != nil {
		return d, fmt.Errorf("diskinfo %s: %w", name, err)
	}

	for _, line := range strings.Split(string(out), "\n") {
		value, comment, ok := strings.Cut(strings.TrimSpace(line), "#")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch comment = strings.TrimSpace(comment); {
		case strings.HasPrefix(comment, "sectorsize"):
			d.SectorSize, _ = strconv.ParseInt(value, 10, 64)
		case strings.HasPrefix(comment, "mediasize in bytes"):
			d.MediaSize, _ = strconv.ParseInt(value, 10, 64)
		case strings.HasPrefix(comment, "mediasize in sectors"):
			d.Sectors, _ = strconv.ParseInt(value, 10, 64)
		case strings.HasPrefix(comment, "stripesize"):
			d.Stripe, _ = strconv.ParseInt(value, 10, 64)
		case strings.HasPrefix(comment, "Disk descr"):
			d.Descr = value
		case strings.HasPrefix(comment, "Rotation rate"):
			if n, err := strconv.Atoi(value); err == nil {
				d.Rotation = n
			}
		}
	}
	if d.MediaSize <= 0 {
		return d, fmt.Errorf("%s: no media", name)
	}
	return d, nil
}

// lessDevice orders ada0 before ada10 before nvd0.
func lessDevice(a, b string) bool {
	pa, na := splitDevice(a)
	pb, nb := splitDevice(b)
	if pa != pb {
		return pa < pb
	}
	return na < nb
}

func splitDevice(s string) (string, int) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	n, _ := strconv.Atoi(s[i:])
	return s[:i], n
}

// HumanBytes renders a byte count in the usual binary units.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 5; v /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	format := "%.0f%c"
	if v < 10 {
		format = "%.1f%c"
	}
	return fmt.Sprintf(format, v, "KMGTPE"[exp])
}
