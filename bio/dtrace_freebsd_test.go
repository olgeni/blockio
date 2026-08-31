package bio

import (
	"strings"
	"testing"
)

func TestScriptCarriesGeometry(t *testing.T) {
	s := Script([]Disk{{Name: "ada0", MediaSize: 1000204886016}}, 100, 4096)
	for _, want := range []string{
		`media["ada0"] = 1000204886016;`,
		`track["ada0"] = 1;`,
		"tick-100ms",
		"inline int BUCKETS = 4096;",
		"int64_t media[string];",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script is missing %q", want)
		}
	}
	// %@d must survive the Sprintf that builds the script.
	if !strings.Contains(s, `printa("C %s %d %d %@d\n", @cells);`) {
		t.Errorf("printa line mangled:\n%s", s)
	}
}
