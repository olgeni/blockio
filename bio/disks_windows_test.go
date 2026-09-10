//go:build amd64 || arm64

package bio

import (
	"encoding/binary"
	"testing"
)

func TestDescribe(t *testing.T) {
	buf := make([]byte, 128)
	binary.LittleEndian.PutUint32(buf[12:], 64) // VendorIdOffset
	binary.LittleEndian.PutUint32(buf[16:], 80) // ProductIdOffset
	binary.LittleEndian.PutUint32(buf[28:], busTypeFileBackedVirtual)
	copy(buf[64:], "Msft    \x00")
	copy(buf[80:], "Virtual Disk    \x00")

	if descr, bus := describe(buf); descr != "Msft Virtual Disk" || bus != busTypeFileBackedVirtual {
		t.Errorf("describe = %q %#x, want %q %#x", descr, bus, "Msft Virtual Disk", busTypeFileBackedVirtual)
	}

	// no vendor, which is how an NVMe disk usually comes
	binary.LittleEndian.PutUint32(buf[12:], 0)
	if descr, _ := describe(buf); descr != "Virtual Disk" {
		t.Errorf("describe without a vendor = %q", descr)
	}
	// a product offset past the end of what came back
	if descr, _ := describe(buf[:70]); descr != "" {
		t.Errorf("describe with the strings cut off = %q", descr)
	}
	if descr, bus := describe(buf[:20]); descr != "" || bus != 0 {
		t.Errorf("describe of a short descriptor = %q %#x", descr, bus)
	}
}

func TestDiskNumber(t *testing.T) {
	for in, want := range map[string]uint32{"disk0": 0, "disk17": 17} {
		if got, ok := diskNumber(in); !ok || got != want {
			t.Errorf("diskNumber(%q) = %d, %v, want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"disk", "disk-1", "disk1s1", "ada0", "PhysicalDrive0"} {
		if _, ok := diskNumber(in); ok {
			t.Errorf("diskNumber(%q) should not parse", in)
		}
	}
}

func TestDeviceNameWindows(t *testing.T) {
	for in, want := range map[string]string{
		`\\.\PhysicalDrive1`: "disk1",
		"PhysicalDrive12":    "disk12",
		"PHYSICALDRIVE3":     "disk3",
		"Disk2":              "disk2",
		"disk0":              "disk0",
	} {
		if got := DeviceName(in); got != want {
			t.Errorf("DeviceName(%q) = %q, want %q", in, got, want)
		}
	}
}
