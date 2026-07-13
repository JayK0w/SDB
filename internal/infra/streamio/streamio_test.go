package streamio

import (
	"strings"
	"testing"
)

func TestLineWriterDecoupeLesBlocs(t *testing.T) {
	var lines []string
	w := &LineWriter{Emit: func(l string) { lines = append(lines, l) }}

	w.Write([]byte(`{"message_`))
	w.Write([]byte("type\":\"status\"}\r\n"))
	w.Write([]byte("ligne2\nligne3\n"))
	w.Write([]byte("reste"))
	w.Flush()

	want := []string{`{"message_type":"status"}`, "ligne2", "ligne3", "reste"}
	if len(lines) != len(want) {
		t.Fatalf("%d lignes %q, attendu %d", len(lines), lines, len(want))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("ligne %d = %q, attendu %q", i, lines[i], want[i])
		}
	}
}

func TestBoundedBufferTronque(t *testing.T) {
	b := NewBounded(4)
	b.Write([]byte("abcdef"))
	got := b.String()
	if got[:4] != "abcd" || !strings.Contains(got, "tronqués") {
		t.Fatalf("String() = %q", got)
	}
}
