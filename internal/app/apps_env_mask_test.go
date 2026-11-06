package app

import "testing"

func TestMaskAroundRefs(t *testing.T) {
	for in, want := range map[string]string{
		"${{postgres.DATABASE_URL}}":                    "${{postgres.DATABASE_URL}}",
		"http://${{api.HOST}}:8080":                     "••••${{api.HOST}}••••",
		"postgres://u:secret@${{db.HOST}}/${{db.NAME}}": "••••${{db.HOST}}••••${{db.NAME}}",
		"${{a.X}}${{b.Y}}":                              "${{a.X}}${{b.Y}}",
	} {
		if got := maskAroundRefs(in); got != want {
			t.Errorf("maskAroundRefs(%q) = %q, want %q", in, got, want)
		}
	}
}
