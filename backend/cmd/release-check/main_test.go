package main

import (
	"encoding/binary"
	"testing"
)

func TestProgramDataAuthorityAndMalformedState(t *testing.T) {
	d := make([]byte, 49)
	binary.LittleEndian.PutUint32(d, 3)
	binary.LittleEndian.PutUint64(d[4:12], 123)
	d[12] = 1
	copy(d[13:45], loader[:])
	copy(d[45:], []byte{1, 2, 3, 4})
	slot, authority, code, err := programData(d)
	if err != nil || slot != 123 || authority != loader.String() || len(code) != 4 {
		t.Fatalf("bad parsed deployment: %d %s %v %v", slot, authority, code, err)
	}
	d[12] = 0
	if _, authority, _, err := programData(d); err != nil || authority != "immutable" {
		t.Fatal("immutable authority not recognized")
	}
	d[12] = 2
	if _, _, _, err := programData(d); err == nil {
		t.Fatal("invalid authority flag accepted")
	}
	if _, _, _, err := programData(d[:12]); err == nil {
		t.Fatal("truncated metadata accepted")
	}
}
