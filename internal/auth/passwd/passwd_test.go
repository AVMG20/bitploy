package passwd

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h, err := Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		password string
		hash     string
		want     bool
		wantErr  bool
	}{
		{"right password", "correct horse battery", h, true, false},
		{"wrong password", "correct horse batterY", h, false, false},
		{"empty password", "", h, false, false},
		{"prefix of password", "correct horse", h, false, false},
		{"not a hash", "x", "plaintext", false, true},
		{"wrong algorithm", "x", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNo", false, true},
		{"bad base64", "x", "$argon2id$v=19$m=19456,t=2,p=1$!!!$!!!", false, true},
		{"absurd memory", "x", "$argon2id$v=19$m=999999999,t=2,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNo", false, true},
		{"empty string", "x", "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Verify(tt.password, tt.hash)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("Verify = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHashFormatMeetsOWASPMinimum(t *testing.T) {
	h, err := Hash("some password here")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected hash parameters: %s", h)
	}
	if MemoryKiB < 19456 || Time < 2 || Threads < 1 {
		t.Fatalf("parameters below the OWASP minimum: m=%d t=%d p=%d", MemoryKiB, Time, Threads)
	}
}

func TestHashesAreSalted(t *testing.T) {
	a, _ := Hash("same password")
	b, _ := Hash("same password")
	if a == b {
		t.Fatal("two hashes of the same password are identical: salt is missing")
	}
}

func TestDummyIsVerifiable(t *testing.T) {
	ok, err := Verify("anything", Dummy())
	if err != nil || ok {
		t.Fatalf("dummy hash should parse and not match: ok=%v err=%v", ok, err)
	}
}
