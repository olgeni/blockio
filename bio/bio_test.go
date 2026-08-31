package bio

import "testing"

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:             "0B",
		999:           "999B",
		1024:          "1.0K",
		10 * 1024:     "10K",
		1000204886016: "932G",
	}
	for in, want := range cases {
		if got := HumanBytes(in); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestLessDevice(t *testing.T) {
	if !lessDevice("ada2", "ada10") {
		t.Error("ada2 should sort before ada10")
	}
	if !lessDevice("ada10", "nvd0") {
		t.Error("ada10 should sort before nvd0")
	}
	if !lessDevice("disk2", "disk10") {
		t.Error("disk2 should sort before disk10")
	}
}

func TestParseSourceKind(t *testing.T) {
	for in, want := range map[string]SourceKind{
		"":         SourceAuto,
		"auto":     SourceAuto,
		"DTrace":   SourceDtrace,
		"fs_usage": SourceFSUsage,
	} {
		got, err := ParseSourceKind(in)
		if err != nil || got != want {
			t.Errorf("ParseSourceKind(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	if _, err := ParseSourceKind("ktrace"); err == nil {
		t.Error("ktrace should not parse")
	}
}
