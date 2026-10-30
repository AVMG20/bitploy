package ids

import "testing"

func TestBase62BytesHasAFixedWidth(t *testing.T) {
	for i := 0; i < 5000; i++ {
		if s := Base62Bytes(32); len(s) != 43 {
			t.Fatalf("Base62Bytes(32) = %q (%d characters), want 43", s, len(s))
		}
	}
}
