package adc

import (
	"bytes"
	"errors"
	"runtime"
	"testing"
)

// allocated reports how many bytes f allocated, by runtime.MemStats.
func allocated(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// naive is the byte-at-a-time decoder this package used before the size
// walk, kept as the oracle for the differential test and the fuzz target.
// limit stops it early so that a bomb does not run it out of memory.
func naive(src []byte, limit int) ([]byte, error) {
	var out []byte
	for i := 0; i < len(src); {
		op := src[i]
		var n, off int
		switch {
		case op&0x80 != 0:
			n = int(op&0x7F) + 1
			i++
			if i+n > len(src) {
				return nil, ErrTruncated
			}
			if len(out)+n > limit {
				return nil, ErrTooLarge
			}
			out = append(out, src[i:i+n]...)
			i += n
			continue
		case op&0x40 != 0:
			if i+3 > len(src) {
				return nil, ErrTruncated
			}
			n = int(op&0x3F) + 4
			off = (int(src[i+1])<<8 | int(src[i+2])) + 1
			i += 3
		default:
			if i+2 > len(src) {
				return nil, ErrTruncated
			}
			n = int(op&0x3C)>>2 + 3
			off = (int(op&0x03)<<8 | int(src[i+1])) + 1
			i += 2
		}
		if off > len(out) {
			return nil, ErrOffset
		}
		if len(out)+n > limit {
			return nil, ErrTooLarge
		}
		for j := 0; j < n; j++ {
			out = append(out, out[len(out)-off])
		}
	}
	return out, nil
}

// bombStream is one literal byte followed by count long matches of 67 bytes
// each: 2+3*count input bytes for 1+67*count output bytes.
func bombStream(count int) []byte {
	src := make([]byte, 0, 2+3*count)
	src = append(src, 0x80, 'x')
	for i := 0; i < count; i++ {
		src = append(src, 0x7F, 0x00, 0x00)
	}
	return src
}

// TestBombRefusedWithoutAllocating plants streams that ask for far more than
// the limit and checks that the refusal costs nothing like what they asked.
// The unbounded decoder allocated 3x the input up front and then grew with the
// output, so a 30 KB stream cost a megabyte before it was refused.
func TestBombRefusedWithoutAllocating(t *testing.T) {
	src := bombStream(10000) // 30 002 bytes asking for 670 001
	for _, limit := range []int{0, 1, 4096, 100000} {
		var err error
		cost := allocated(func() { _, err = DecompressLimit(src, limit) })
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("limit %d: err = %v, want ErrTooLarge", limit, err)
		}
		if cost > 64<<10 {
			t.Errorf("limit %d: refusal allocated %d bytes, want < 64 KiB", limit, cost)
		}
	}
}

// TestAllocationIsTheOutput: a 3-byte opcode asks for 67 bytes, so the
// allocation of a legitimate decode must be the output and nothing more, not
// a guess at a ratio.
func TestAllocationIsTheOutput(t *testing.T) {
	src := bombStream(10000)
	var out []byte
	cost := allocated(func() {
		var err error
		if out, err = DecompressLimit(src, 1<<20); err != nil {
			t.Fatal(err)
		}
	})
	if len(out) != 1+67*10000 {
		t.Fatalf("decoded %d bytes, want %d", len(out), 1+67*10000)
	}
	if cost > uint64(len(out))+4096 {
		t.Errorf("decode allocated %d bytes for %d of output", cost, len(out))
	}
}

// TestLimitIsExact: output of exactly max passes, one more byte is refused.
func TestLimitIsExact(t *testing.T) {
	src := bombStream(3) // 202 bytes out
	if out, err := DecompressLimit(src, 202); err != nil || len(out) != 202 {
		t.Fatalf("at the limit: %d bytes, %v", len(out), err)
	}
	if _, err := DecompressLimit(src, 201); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("one over the limit: err = %v, want ErrTooLarge", err)
	}
	// A negative ceiling is a zero ceiling: nothing but the empty stream.
	if _, err := DecompressLimit(src, -5); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("negative limit: err = %v, want ErrTooLarge", err)
	}
	if out, err := DecompressLimit(nil, -5); err != nil || len(out) != 0 {
		t.Fatalf("empty stream under a negative limit: %d bytes, %v", len(out), err)
	}
	if out, err := DecompressLimit(nil, 0); err != nil || len(out) != 0 {
		t.Fatalf("empty stream under limit 0: %d bytes, %v", len(out), err)
	}
}

func TestDefaultMaxOutput(t *testing.T) {
	if DefaultMaxOutput != 1<<30 {
		t.Fatalf("DefaultMaxOutput = %d, want 1 GiB", DefaultMaxOutput)
	}
	// The default entry point is the limited one at the default ceiling.
	src := bombStream(5)
	a, err := Decompress(src)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DecompressLimit(src, DefaultMaxOutput)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("Decompress and DecompressLimit(DefaultMaxOutput) disagree: %v", err)
	}
}

// TestSizeWalkIsNotFooled: the size walk and the decode must agree, and the
// walk must not be satisfied by a stream whose offsets reach back further than
// the output it has seen. A stream is cut at every length to cover every
// operand boundary.
func TestMatchesNaiveAtEveryCut(t *testing.T) {
	full, err := golden.ReadFile("testdata/apple-hdiutil.adc")
	if err != nil {
		t.Fatal(err)
	}
	for cut := 0; cut <= len(full); cut++ {
		want, werr := naive(full[:cut], 1<<20)
		got, gerr := DecompressLimit(full[:cut], 1<<20)
		if !errors.Is(gerr, werr) && werr != gerr {
			t.Fatalf("cut %d: err = %v, naive says %v", cut, gerr, werr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("cut %d: output differs from the naive decoder", cut)
		}
	}
}

// TestOverlapPeriods covers copyMatch where the match is longer than its
// offset by many periods and by a partial one, for offsets that do not divide
// the length.
func TestOverlapPeriods(t *testing.T) {
	for off := 1; off <= 9; off++ {
		for n := 4; n <= 67; n++ {
			src := []byte{0x80 | byte(off-1)}
			for i := 0; i < off; i++ {
				src = append(src, byte('a'+i))
			}
			src = append(src, 0x40|byte(n-4), 0x00, byte(off-1))
			got, err := Decompress(src)
			if err != nil {
				t.Fatalf("off %d n %d: %v", off, n, err)
			}
			want, _ := naive(src, 1<<20)
			if !bytes.Equal(got, want) {
				t.Fatalf("off %d n %d: got %q, want %q", off, n, got, want)
			}
		}
	}
}

// TestFuzzCrashers pins the inputs the fuzzer or a hand probe produced, as
// named tests, because the emulated CI lanes run a compiled test binary away
// from testdata/ and would otherwise never run them.
func TestFuzzCrashers(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  []byte
		want error
	}{
		// Long match with the largest offset (65536) at the start of the output.
		{"max offset on an empty output", []byte{0x7F, 0xFF, 0xFF}, ErrOffset},
		// A literal that claims 128 bytes with one present.
		{"literal claims more than remains", []byte{0xFF, 'a'}, ErrTruncated},
		// Opcode byte alone at the end after a valid literal.
		{"dangling long opcode", []byte{0x80, 'a', 0x7F}, ErrTruncated},
		{"dangling short opcode", []byte{0x80, 'a', 0x3C}, ErrTruncated},
	} {
		if _, err := Decompress(tc.src); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

// FuzzDecompress feeds arbitrary streams to the decoder under a small ceiling
// and checks it against the naive decoder: same error, same bytes, and never
// more output than the ceiling.
func FuzzDecompress(f *testing.F) {
	apple, err := golden.ReadFile("testdata/apple-hdiutil.adc")
	if err == nil {
		f.Add(apple)
	}
	f.Add([]byte{})
	f.Add(bombStream(4))
	f.Add([]byte{0x80, 'A', 0x00, 0x00})
	f.Add([]byte{0x7F, 0xFF, 0xFF})
	f.Fuzz(func(t *testing.T, src []byte) {
		const limit = 1 << 16
		got, gerr := DecompressLimit(src, limit)
		want, werr := naive(src, limit)
		if gerr != werr {
			t.Fatalf("err = %v, naive says %v", gerr, werr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("output differs from the naive decoder: %d vs %d bytes", len(got), len(want))
		}
		if len(got) > limit {
			t.Fatalf("%d bytes of output past the %d ceiling", len(got), limit)
		}
	})
}
