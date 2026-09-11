package wire

import (
	"bytes"
	"io"
	"testing"
)

func TestMaskChangesWithSequenceAndRoundTrips(t *testing.T) {
	var sid SessionID
	for i := range sid {
		sid[i] = byte(i + 1)
	}
	plain := bytes.Repeat([]byte("DragonTCP"), 100)
	a := append([]byte(nil), plain...)
	b := append([]byte(nil), plain...)
	MaskInPlace(a, sid, ModeUpload, 1, false)
	MaskInPlace(b, sid, ModeUpload, 2, false)
	if bytes.Equal(a, b) {
		t.Fatal("different sequences produced identical wire bytes")
	}
	MaskInPlace(a, sid, ModeUpload, 1, false)
	if !bytes.Equal(a, plain) {
		t.Fatal("mask did not round-trip")
	}
}

func TestBinaryHeaderProfilesRoundTrip(t *testing.T) {
	var sid SessionID
	for i := range sid {
		sid[i] = byte(i + 1)
	}
	for n := 0; n < 256; n += 8 {
		mask := byte(n)
		var request bytes.Buffer
		if err := WriteRequestProfile(&request, ModeUpload, sid, 42, []byte("payload"), mask); err != nil {
			t.Fatal(err)
		}
		if got := request.Bytes()[0]; got != ModeUpload^mask {
			t.Fatalf("mask %02x first byte=%02x", mask, got)
		}
		req, err := ReadRequestProfile(&request, mask)
		if err != nil {
			t.Fatalf("mask %02x: %v", mask, err)
		}
		if req.Mode != ModeUpload || req.Seq != 42 || !bytes.Equal(req.Payload, []byte("payload")) {
			t.Fatalf("mask %02x request did not round-trip", mask)
		}

		var response bytes.Buffer
		if err := WriteResponseProfile(&response, StatusOK, []byte("ok"), mask); err != nil {
			t.Fatal(err)
		}
		status, body, err := ReadResponseProfile(&response, mask)
		if err != nil || status != StatusOK || !bytes.Equal(body, []byte("ok")) {
			t.Fatalf("mask %02x response did not round-trip: status=%d body=%q err=%v", mask, status, body, err)
		}
	}
}

type profiledBuffer struct {
	bytes.Buffer
	mask byte
}

func (b *profiledBuffer) HeaderMask() byte { return b.mask }

func TestServerResponseUsesConnectionProfile(t *testing.T) {
	profiled := &profiledBuffer{mask: 0xa0}
	if err := WriteResponse(profiled, StatusOK, []byte("ok")); err != nil {
		t.Fatal(err)
	}
	if got := profiled.Bytes()[0]; got != StatusOK^profiled.mask {
		t.Fatalf("first byte=%02x, want %02x", got, StatusOK^profiled.mask)
	}
}

func BenchmarkMask1MiB(b *testing.B) {
	var sid SessionID
	data := make([]byte, 1024*1024)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MaskInPlace(data, sid, ModeUpload, uint64(i), false)
	}
}

func BenchmarkWriteRequest1MiB(b *testing.B) {
	var sid SessionID
	data := make([]byte, 1024*1024)
	for _, tc := range []struct {
		name  string
		clear bool
	}{{"sha256-compat", false}, {"clear", true}} {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := WriteRequestProfileEncoding(io.Discard, ModeUpload, sid, uint64(i), data, 0, tc.clear); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestProbeBurstCountSinglePoller(t *testing.T) {
	for _, chunk := range []int{0, 1, 1024, 128 * 1024, 512 * 1024, 1024 * 1024} {
		if got := ProbeBurstCount(chunk); got != 1 {
			t.Fatalf("ProbeBurstCount(%d)=%d, want 1", chunk, got)
		}
	}
}
