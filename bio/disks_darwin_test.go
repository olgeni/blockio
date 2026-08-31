package bio

import "testing"

func TestDiskFromInfo(t *testing.T) {
	cases := []struct {
		name  string
		info  diskInfo
		descr string
		image bool
	}{
		{
			name:  "disk0",
			info:  diskInfo{Size: 1000555581440, DeviceBlockSize: 4096, MediaName: "APPLE SSD AP1024Z", SolidState: true, IORegistryEntryName: "APPLE SSD AP1024Z Media"},
			descr: "APPLE SSD AP1024Z",
		},
		{
			name:  "disk3",
			info:  diskInfo{Size: 994662584320, DeviceBlockSize: 4096, MediaName: "APPLE SSD AP1024Z", SolidState: true, IORegistryEntryName: "AppleAPFSMedia"},
			descr: "APPLE SSD AP1024Z APFS container",
		},
		{
			// a mounted image, and the container synthesized on top of it:
			// both inherit the name, so both are left unselected
			name:  "disk4",
			info:  diskInfo{Size: 17572070912, DeviceBlockSize: 512, MediaName: "Disk Image", IORegistryEntryName: "Apple Disk Image Media"},
			descr: "disk image",
			image: true,
		},
		{
			name:  "disk5",
			info:  diskInfo{Size: 17572036608, DeviceBlockSize: 4096, MediaName: "Disk Image", IORegistryEntryName: "AppleAPFSMedia"},
			descr: "Disk Image APFS container",
			image: true,
		},
	}

	for _, c := range cases {
		d := c.info.disk(c.name)
		if d.Descr != c.descr || d.Image != c.image {
			t.Errorf("%s: descr = %q, image = %v, want %q, %v", c.name, d.Descr, d.Image, c.descr, c.image)
		}
		if d.SectorSize > 0 && d.Sectors != c.info.Size/c.info.DeviceBlockSize {
			t.Errorf("%s: sectors = %d", c.name, d.Sectors)
		}
	}
}

func TestRotationIsUnknownForSpinningDisks(t *testing.T) {
	// diskutil reports no RPM, so only "solid state" is ever known.
	if got := (diskInfo{SolidState: true}).disk("disk0").Rotation; got != 0 {
		t.Errorf("solid state rotation = %d, want 0", got)
	}
	if got := (diskInfo{}).disk("disk0").Rotation; got != -1 {
		t.Errorf("unknown rotation = %d, want -1", got)
	}
}
