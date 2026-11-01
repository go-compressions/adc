package adc

import (
	"bytes"
	"testing"
)

// benchStream is the Apple fixture repeated: a valid stream, because every
// match only reaches back into output that already exists.
func benchStream(b *testing.B, copies int) []byte {
	b.Helper()
	src, err := golden.ReadFile("testdata/apple-hdiutil.adc")
	if err != nil {
		b.Fatal(err)
	}
	return bytes.Repeat(src, copies)
}

func BenchmarkDecompressApple(b *testing.B) {
	src := benchStream(b, 1)
	out, err := Decompress(src)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Decompress(src); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecompressApple1000(b *testing.B) {
	src := benchStream(b, 1000)
	out, err := Decompress(src)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Decompress(src); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecompressRuns is the shape a long match is for: one byte, then
// 67-byte copies of it.
func BenchmarkDecompressRuns(b *testing.B) {
	src := []byte{0x80, 'x'}
	for i := 0; i < 20000; i++ {
		src = append(src, 0x7F, 0x00, 0x00)
	}
	out, err := Decompress(src)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Decompress(src); err != nil {
			b.Fatal(err)
		}
	}
}
