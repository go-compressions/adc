// Package adc decodes Apple Data Compression (ADC), the byte-oriented LZ77
// variant Apple uses for the "UDCO" flavour of UDIF disk images and, before
// that, for resources in classic Mac OS.
//
// The format has no header, no checksum and no stored output length: a stream
// is a bare sequence of three opcodes, and it ends when its bytes run out. A
// caller that knows how long the output should be must check that itself.
//
//	out, err := adc.Decompress(src)
//
// CGO is not used, so this builds for every target Go builds for.
//
// # The opcodes
//
// The first byte of each opcode says which one it is, and the format was read
// off streams written by Apple's own hdiutil rather than from any
// implementation of it:
//
//	1xxxxxxx                    literal run, (x+1) bytes follow, 1..128
//	01xxxxxx hhhhhhhh llllllll  match, length (x+4) 4..67,  offset (hl+1) 1..65536
//	00xxxxyy yyyyyyyy           match, length (x+3) 3..18,  offset (y+1)  1..1024
//
// An offset counts backwards from the end of the output produced so far, so 1
// is the byte just written. A match may be longer than its offset, in which
// case it repeats what it has already copied; that is the usual way a run of
// one byte is coded, and it is why the copy repeats the offset's period
// instead of moving a slice.
//
// # Untrusted input
//
// The stream carries no length, so the only thing that says how much a
// decode produces is the decode itself, and one opcode byte can ask for far
// more output than it costs: a three-byte long match yields up to 67 bytes
// (22.3 per input byte), a two-byte short match up to 18. Decompress therefore
// walks the opcodes once without copying, checks every operand and the running
// output size, and allocates exactly once, only after the size is known to be
// within the ceiling. The ceiling is [DefaultMaxOutput] for [Decompress] and the
// caller's choice for [DecompressLimit]; a caller that knows the real size (a
// UDIF chunk says how many sectors it holds) should pass it.
package adc

import "errors"

// ErrTruncated is returned when an opcode's operands, or a literal run's
// bytes, reach past the end of the input.
var ErrTruncated = errors.New("adc: truncated stream")

// ErrOffset is returned when a match points further back than the output
// written so far, which no stream produced by a correct encoder ever does.
var ErrOffset = errors.New("adc: match offset reaches before the start of the output")

// ErrTooLarge is returned when the stream would produce more output than the
// ceiling allows. It is returned before any output is allocated.
var ErrTooLarge = errors.New("adc: output exceeds the size limit")

// DefaultMaxOutput is the ceiling [Decompress] applies: 1 GiB. ADC amplifies
// by up to 22.3x, so a stream of 48 MB can ask for it.
const DefaultMaxOutput = 1 << 30

// Decompress returns the bytes the stream encodes, refusing a stream that
// would produce more than [DefaultMaxOutput] bytes with [ErrTooLarge]. An empty
// input gives an empty output and no error: the format ends by exhaustion, so a
// stream of no opcodes is a stream of no bytes.
func Decompress(src []byte) ([]byte, error) {
	return DecompressLimit(src, DefaultMaxOutput)
}

// DecompressLimit is [Decompress] with a caller-chosen ceiling: a stream whose
// output would exceed max bytes gives [ErrTooLarge] and allocates nothing
// proportional to what it declared. A max below zero is taken as zero.
func DecompressLimit(src []byte, max int) ([]byte, error) {
	size, err := outputSize(src, max)
	if err != nil {
		return nil, err
	}
	out := make([]byte, size)
	pos := 0
	for i := 0; i < len(src); {
		op := src[i]
		switch {
		case op&0x80 != 0: // literal run
			n := int(op&0x7F) + 1
			pos += copy(out[pos:], src[i+1:i+1+n])
			i += 1 + n

		case op&0x40 != 0: // long match, two offset bytes
			n := int(op&0x3F) + 4
			off := (int(src[i+1])<<8 | int(src[i+2])) + 1
			i += 3
			pos = copyMatch(out, pos, off, n)

		default: // short match, offset's low byte follows
			n := int(op&0x3C)>>2 + 3
			off := (int(op&0x03)<<8 | int(src[i+1])) + 1
			i += 2
			pos = copyMatch(out, pos, off, n)
		}
	}
	return out, nil
}

// outputSize walks the opcodes without copying anything and returns how many
// bytes the stream produces. It fails exactly where a decode would (truncated
// operands, an offset before the start of the output) and with ErrTooLarge as
// soon as the running total passes max, so the cost of refusing a bomb is one
// pass over the input. The total is kept in int64 so that it cannot wrap on a
// 32-bit target; it never exceeds max once it is returned.
func outputSize(src []byte, max int) (int, error) {
	limit := int64(max)
	if limit < 0 {
		limit = 0
	}
	var total int64
	for i := 0; i < len(src); {
		op := src[i]
		var n int
		switch {
		case op&0x80 != 0:
			n = int(op&0x7F) + 1
			if n > len(src)-i-1 {
				return 0, ErrTruncated
			}
			i += 1 + n
		case op&0x40 != 0:
			if len(src)-i < 3 {
				return 0, ErrTruncated
			}
			n = int(op&0x3F) + 4
			if int64(int(src[i+1])<<8|int(src[i+2]))+1 > total {
				return 0, ErrOffset
			}
			i += 3
		default:
			if len(src)-i < 2 {
				return 0, ErrTruncated
			}
			n = int(op&0x3C)>>2 + 3
			if int64(int(op&0x03)<<8|int(src[i+1]))+1 > total {
				return 0, ErrOffset
			}
			i += 2
		}
		total += int64(n)
		if total > limit {
			return 0, ErrTooLarge
		}
	}
	return int(total), nil
}

// copyMatch writes n bytes taken off bytes back from pos and returns the new
// position. A match may overlap what it copies (a run of one byte is an offset
// of 1), so when n exceeds off the copy repeats the off-byte period: each
// round copies everything written since the match began, doubling.
// outputSize has already proved that off <= pos and pos+n <= len(out).
func copyMatch(out []byte, pos, off, n int) int {
	src := pos - off
	for k := 0; k < n; {
		k += copy(out[pos+k:pos+n], out[src:pos+k])
	}
	return pos + n
}
