package deploy

import "testing"

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{
		"0B":      0,
		"512kB":   512_000,
		"1.23GB":  1_230_000_000,
		"1.5GiB":  1_610_612_736,
		"20 MB":   20_000_000,
		"3MiB":    3 << 20,
		"42":      42,
		" 2.0TB ": 2_000_000_000_000,
	} {
		got, ok := ParseSize(in)
		if !ok || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "GB", "1.2XB", "-1GB", "abc"} {
		if _, ok := ParseSize(in); ok {
			t.Errorf("ParseSize(%q) ok", in)
		}
	}
}

func TestPruneTotal(t *testing.T) {
	out := `ID                                              RECLAIMABLE     SIZE            LAST ACCESSED
yk3fx9r0k1q5vzj0x2vbtqv1y*                      true            1.03GB          2 weeks ago
pz0n0w6m0bl2ke1ebgvx9b0x7                       true            215.4MB         3 days ago
Total:	1.245GB
`
	if got := PruneTotal(out); got != 1_245_000_000 {
		t.Errorf("PruneTotal = %d", got)
	}
	if got := PruneTotal("nothing to prune\n"); got != 0 {
		t.Errorf("PruneTotal of empty output = %d", got)
	}
}
