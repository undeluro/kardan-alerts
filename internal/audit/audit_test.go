package audit

import (
	"testing"
	"time"
)

func TestHashChain(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 123456000, time.UTC)
	a := Entry{Seq: 1, At: at, Actor: "sam", Action: "alert.ack", Subject: "alert:X", Detail: []byte(`{}`), PrevHash: Genesis}
	a.Hash = Hash(a)
	b := Entry{Seq: 2, At: at, Actor: "maria", Action: "action.approved", Subject: "run:Y", Detail: []byte(`{"step":2}`), PrevHash: a.Hash}
	b.Hash = Hash(b)
	if Hash(b) != b.Hash || len(b.Hash) != 64 {
		t.Fatal("hash must be stable")
	}
	tampered := a
	tampered.Actor = "mallory"
	if Hash(tampered) == a.Hash {
		t.Error("changing the actor must change the hash")
	}
	// Moving text between fields must not produce the same hash.
	x := Entry{Seq: 1, At: at, Actor: "a\"\nb", Action: "c", PrevHash: Genesis}
	y := Entry{Seq: 1, At: at, Actor: "a", Action: "\"\nbc", PrevHash: Genesis}
	if Hash(x) == Hash(y) {
		t.Error("field boundaries must be unambiguous")
	}
}
