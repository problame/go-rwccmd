package rwccmd

import (
	"bytes"
	"io"
	"io/ioutil"
)

const stderrWriterLength = 2*32*1024
var stderrWriterDots = []byte("\n...\n")

type stderrWriter struct {
	begin bytes.Buffer
	end bytes.Buffer
	discarded bool
}

func (w *stderrWriter) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	beginLeft := stderrWriterLength - w.begin.Len()
	if beginLeft == 0 { // write to back
		writeBytes := len(p)
		if writeBytes > stderrWriterLength {
			w.discarded = true
			writeBytes = stderrWriterLength
		}
		discardBytes := (w.end.Len() + writeBytes) - stderrWriterLength
		if discardBytes > 0 {
			w.discarded = true
			io.CopyN(ioutil.Discard, &w.end, int64(discardBytes))
		}
		w.end.Write(p[len(p)-writeBytes:len(p)])
		return len(p), nil
	}
	// write to front
	if beginLeft >= len(p) {
		return w.begin.Write(p)
	}

	w.begin.Write(p[0:beginLeft])
	w.Write(p[beginLeft:]) // recursion
	return len(p), nil
}

func (w *stderrWriter) Bytes() []byte {
	if w.end.Len() == 0 {
		return w.begin.Bytes()
	}
	var b bytes.Buffer
	io.Copy(&b, &w.begin)
	if w.discarded {
		b.Write(stderrWriterDots)
		io.CopyN(ioutil.Discard, &w.end, int64(len(stderrWriterDots)))
	}
	io.Copy(&b, &w.end)
	return b.Bytes()
}
