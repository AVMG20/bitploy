package passwd

import (
	"errors"
	"strings"
	"testing"
	"time"
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

func TestQueueIsCappedAndFailsFast(t *testing.T) {
	h, err := Hash("some password here")
	if err != nil {
		t.Fatal(err)
	}
	oldSem, oldMax := sem, MaxWaiting
	sem = make(chan struct{}, 1)
	MaxWaiting = 2
	t.Cleanup(func() { sem, MaxWaiting = oldSem, oldMax })

	sem <- struct{}{} // the one slot is busy
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := Verify("some password here", h); done <- err }()
	}
	for waiting.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	if _, err := Verify("x", h); !errors.Is(err, ErrBusy) {
		t.Fatalf("third waiter: want ErrBusy, got %v", err)
	}
	if _, err := Hash("another password"); !errors.Is(err, ErrBusy) {
		t.Fatalf("Hash while queue is full: want ErrBusy, got %v", err)
	}
	<-sem // free the slot; the two queued checks finish
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("queued check failed: %v", err)
		}
	}
	if waiting.Load() != 0 {
		t.Fatalf("waiting counter leaked: %d", waiting.Load())
	}
}
