package rwccmd

import (
	"testing"
	"bytes"
)

var table = []struct{Length int; Split bool; BytesLength int}{
	{0, false, 0},
	{1, false, 1},
	{stderrWriterLength - 1, false, stderrWriterLength-1},
	{stderrWriterLength, false, stderrWriterLength},
	{stderrWriterLength+len(stderrWriterDots)-1, false, stderrWriterLength + len(stderrWriterDots) - 1},
	{stderrWriterLength + len(stderrWriterDots) + 1, false, stderrWriterLength + len(stderrWriterDots) + 1},
	{2*stderrWriterLength, false, 2*stderrWriterLength},
	{2*stderrWriterLength + 1, true, 2*stderrWriterLength},
	{2*stderrWriterLength + 2, true, 2*stderrWriterLength},
	{2*stderrWriterLength + len(stderrWriterDots), true, 2*stderrWriterLength},
	{2*stderrWriterLength + len(stderrWriterDots) + 1, true, 2*stderrWriterLength},
	{3*stderrWriterLength, true, 2*stderrWriterLength},

}

var chunkSizes = []int{1,len(stderrWriterDots),stderrWriterLength, 2*stderrWriterLength, 3*stderrWriterLength}

func TestStderrWriter(t *testing.T) {

	t.Logf("stderrWriterLength = %d", stderrWriterLength)
	for _, c := range table {
		forChunk:
		for _, s := range chunkSizes {
			t.Logf("chunkSize = %d ; c = %#v", s, c)
			w := &stderrWriter{}
			// build input
			for i := 0; i < c.Length; {
				var b bytes.Buffer
				for j := 0; j < s && i < c.Length; {
					b.Write([]byte{byte(i)}) // mod 255
					i++
					j++
				}
				expectWritten := b.Len()
				n, err := w.Write(b.Bytes())
				if err != nil || int(n) != expectWritten {
					t.Errorf("unexpected error or not all bytes written: %v, %v != %v", err, n, expectWritten)
					continue forChunk
				}
			}
			o := w.Bytes()
			if c.Split != bytes.Contains(o, stderrWriterDots) {
				t.Errorf("split mismatch")
			}
			if c.Split && !w.discarded {
				t.Errorf("expecting discarded to be true on split")
			}
			if len(o) != c.BytesLength {
				t.Errorf("bytes length mismatch: %d vs expected %d", len(o), c.BytesLength)
			}
			// check byte pattern
			firstSuffixByte := byte(stderrWriterLength)
			if c.Length > 2*stderrWriterLength {
				firstSuffixByte = byte(c.Length - stderrWriterLength + len(stderrWriterDots))
			}
			dotsStartIdx := -1
			if c.Split {
				dotsStartIdx = stderrWriterLength
			}
			for i := 0; i < c.BytesLength; i++ {
				if i < stderrWriterLength {
					if byte(i) != o[i] {
						t.Errorf("broken prefix at %d: %v != %v", i, byte(i), o[i])
					}
				} else if dotsStartIdx >= 0 && (i >= dotsStartIdx && i < dotsStartIdx+len(stderrWriterDots)) {
					if stderrWriterDots[i-dotsStartIdx] != o[i] {
						t.Errorf("expecting dots byte at byte %d", i)
					}
				} else {
					if firstSuffixByte != o[i] {
						t.Errorf("%v", o)
						t.Errorf("broken suffix at %d: %v != %v", i, firstSuffixByte, o[i])
					}
					firstSuffixByte++ // mod 255
				}
			}
		}
	}

}

func TestStderrWriter_EmptyWrite(t *testing.T) {
	w := &stderrWriter{}
	n, err := w.Write([]byte{})
	if n != 0 || err != nil {
		t.Errorf("unexpected byte count or error: %v %v", n, err)
	}
	o := w.Bytes()
	if len(o) != 0 {
		t.Errorf("bytes has nonzero size: %d", len(o))
	}
}
