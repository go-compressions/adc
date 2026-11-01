<p align="center"><img src="https://raw.githubusercontent.com/go-compressions/brand/main/social/go-compressions-adc.png" alt="go-compressions/adc" width="720"></p>

# adc

Decodes **Apple Data Compression** (ADC) — pure Go, `CGO_ENABLED=0`, builds for
every target Go builds for.

```go
out, err := adc.Decompress(src)
```

ADC is what the `UDCO` flavour of a UDIF disk image (`.dmg`) stores its sectors
in, and what classic Mac OS used for compressed resources. `hdiutil` names it
plainly:

```
UDCO - compressed (ADC)
```

## The format

There is no header, no checksum and no stored output length. A stream is a bare
sequence of three opcodes and it ends when its bytes run out, so a caller that
knows how long the output should be has to check that itself — this package
returns what the stream encodes and does not second-guess it.

| first byte | meaning | length | offset |
|---|---|---|---|
| `1xxxxxxx` | literal run, `x+1` bytes follow | 1..128 | — |
| `01xxxxxx hhhhhhhh llllllll` | match | `x+4`, 4..67 | `hl+1`, 1..65536 |
| `00xxxxyy yyyyyyyy` | match | `x+3`, 3..18 | `y+1`, 1..1024 |

An offset counts backwards from the end of the output written so far, so `1` is
the byte just produced. **A match may be longer than its offset**, repeating
what it has already copied; that is how a run of one byte is coded, and it is
why the copy runs a byte at a time rather than as a slice.

## Where the format came from

Off streams written by Apple. A raw image was converted with

```
hdiutil convert raw.img -format UDCO -o out.dmg
```

and the bytes of the ADC run lifted out of the resulting `blkx` table. The test
fixture is that pair — Apple's compressed stream and the plaintext it has to
give back — so nothing here is checked against an encoder of ours. There is no
encoder of ours: nothing asks for one yet.

The fixture is worth nothing unless it reaches all three opcodes, so the test
counts them before trusting the comparison (12 literal runs, 45 long matches, 4
short matches) and fails if any count is zero.

## Errors

| | |
|---|---|
| `ErrTruncated` | an opcode's operands, or a literal run's bytes, reach past the end of the input |
| `ErrOffset` | a match points further back than the output written so far |
| `ErrTooLarge` | the stream would produce more than the ceiling (see below) |

## Untrusted input

ADC amplifies: a 3-byte match opcode yields up to 67 bytes (22.3 per input
byte), and the stream carries no length, so a small file can ask for a great
deal of memory. Before v0.3.0 the decoder guessed `3 x len(src)` and let the
output grow by appending: a 3 MB stream that decodes to 67 MB allocated 300 MB
on the way, and nothing stopped a 48 MB stream from asking for 1 GiB.

```go
out, err := adc.Decompress(src)              // ceiling: adc.DefaultMaxOutput, 1 GiB
out, err := adc.DecompressLimit(src, 1<<20)  // you know the chunk holds 1 MiB
```

The decode is two passes. The first walks the opcodes **without copying**,
checks every operand against the end of the input and every offset against the
output produced so far, and stops with `ErrTooLarge` the moment the running
total passes the ceiling, before anything is allocated. The second allocates
exactly the output once and fills it. The reference decoder in `dmg2img`
(`adc.c`) checks the same bound per opcode against the size the caller
provides; pass the size you know (a UDIF chunk says how many sectors it holds)
to `DecompressLimit`.

Size arithmetic is in `int64`, so nothing wraps on a 32-bit target. A
`FuzzDecompress` target compares the decoder against a naive byte-at-a-time
oracle under a 64 KiB ceiling. The two-pass decode is also faster than the
append loop it replaced (about 4x on the Apple fixture and on long runs).

## Who needs it

[`go-diskimages/dmg`](https://github.com/go-diskimages/dmg) reads a `UDCO`
image with it. Before this existed that package had `0x80000004` labelled LZFSE,
which is in fact ADC, so a real `UDCO` image was handed to the LZFSE decoder and
a real `ULFO` image refused as an unknown type.

## Licence

BSD-3-Clause.
