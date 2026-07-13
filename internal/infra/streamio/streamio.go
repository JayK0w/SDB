// Package streamio : utilitaires de flux partagés par les adaptateurs.
package streamio

import (
	"bytes"
	"strconv"
	"strings"
)

// BoundedBuffer : garde au plus limit octets, compte ce qui déborde.
// Évite qu'une sortie de hook ou de worker n'explose la mémoire.
type BoundedBuffer struct {
	limit   int
	buf     []byte
	dropped int
}

func NewBounded(limit int) *BoundedBuffer { return &BoundedBuffer{limit: limit} }

func (b *BoundedBuffer) Write(p []byte) (int, error) {
	room := b.limit - len(b.buf)
	if room > 0 {
		if len(p) < room {
			room = len(p)
		}
		b.buf = append(b.buf, p[:room]...)
		b.dropped += len(p) - room
	} else {
		b.dropped += len(p)
	}
	return len(p), nil
}

func (b *BoundedBuffer) String() string {
	if b.dropped > 0 {
		return string(b.buf) + "\n... (" + strconv.Itoa(b.dropped) + " octets tronqués)"
	}
	return string(b.buf)
}

// LineWriter : découpe un flux arbitraire en lignes complètes livrées à Emit.
// Permet de parser le JSON de restic pendant que stdcopy écrit par blocs.
type LineWriter struct {
	Emit func(string)
	rem  []byte
}

func (w *LineWriter) Write(p []byte) (int, error) {
	w.rem = append(w.rem, p...)
	for {
		i := bytes.IndexByte(w.rem, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.rem[:i]), "\r")
		w.rem = w.rem[i+1:]
		if line != "" {
			w.Emit(line)
		}
	}
	return len(p), nil
}

// Flush : libère une éventuelle dernière ligne non terminée.
func (w *LineWriter) Flush() {
	if len(w.rem) > 0 {
		w.Emit(string(w.rem))
		w.rem = nil
	}
}
