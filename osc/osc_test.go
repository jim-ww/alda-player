package osc

import (
	"errors"
	"testing"
	"time"

	"github.com/daveyarwood/go-osc/osc"
	"github.com/google/go-cmp/cmp"
)

type decoded struct {
	Address string
	Tags    string
	Args    []Arg
}

func decodeAll(t *testing.T, packet []byte) []decoded {
	t.Helper()
	var d Decoder
	var got []decoded
	err := d.Decode(packet, func(m *Message) error {
		got = append(got, decoded{string(m.Address), string(m.Tags), append([]Arg(nil), m.Args...)})
		return nil
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return got
}

func marshal(t *testing.T, p osc.Packet) []byte {
	t.Helper()
	b, err := p.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeClientMessages(t *testing.T) {
	note := osc.NewMessage("/track/1/midi/note")
	for _, v := range []int32{0, 500, 60, 500, 450, 100} {
		note.Append(v)
	}
	tempo := osc.NewMessage("/system/tempo")
	tempo.Append(int32(0))
	tempo.Append(float32(120.5))
	export := osc.NewMessage("/system/midi/export")
	export.Append("/tmp/x.mid")

	bundle := osc.NewBundle(time.Now())
	bundle.Append(tempo)
	bundle.Append(note)
	bundle.Append(osc.NewMessage("/system/play"))

	tests := []struct {
		name   string
		packet osc.Packet
		want   []decoded
	}{
		{"no args", osc.NewMessage("/ping"), []decoded{{Address: "/ping"}}},
		{"string", export, []decoded{{"/system/midi/export", "s", []Arg{{Type: 's', Bytes: []byte("/tmp/x.mid")}}}}},
		{"bundle", bundle, []decoded{
			{"/system/tempo", "if", []Arg{{Type: 'i'}, {Type: 'f', Float: 120.5}}},
			{"/track/1/midi/note", "iiiiii", []Arg{
				{Type: 'i'}, {Type: 'i', Int: 500}, {Type: 'i', Int: 60},
				{Type: 'i', Int: 500}, {Type: 'i', Int: 450}, {Type: 'i', Int: 100},
			}},
			{Address: "/system/play"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeAll(t, marshal(t, tt.packet))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDecodeOtherTypes(t *testing.T) {
	m := osc.NewMessage("/x")
	m.Append(int64(-5))
	m.Append(3.25)
	m.Append([]byte{1, 2, 3, 4, 5})
	m.Append(true)
	m.Append(nil)
	m.Append(int32(-1))
	got := decodeAll(t, marshal(t, m))
	want := []decoded{{"/x", "hdbTNi", []Arg{
		{Type: 'h', Int: -5}, {Type: 'd', Float: 3.25}, {Type: 'b', Bytes: []byte{1, 2, 3, 4, 5}},
		{Type: 'T'}, {Type: 'N'}, {Type: 'i', Int: -1},
	}}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestDecodeMalformed(t *testing.T) {
	valid := marshal(t, osc.NewMessage("/ping"))
	note := osc.NewMessage("/n")
	note.Append(int32(1))
	b := marshal(t, note)

	tests := []struct {
		name   string
		packet []byte
	}{
		{"empty", nil},
		{"no address slash", []byte("ping\x00\x00\x00\x00")},
		{"unterminated", []byte("/ping")},
		{"truncated padding", valid[:6]},
		{"truncated int", b[:len(b)-2]},
		{"bad tag", []byte("/a\x00\x00,z\x00\x00")},
		{"bundle header", []byte("#bundle\x00\x00")},
		{"bundle size", append([]byte("#bundle\x00\x00\x00\x00\x00\x00\x00\x00\x00"), 0, 0, 0, 99)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Decoder
			err := d.Decode(tt.packet, func(*Message) error { return nil })
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Decode error = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestDecodeStopsOnCallbackError(t *testing.T) {
	bundle := osc.NewBundle(time.Now())
	bundle.Append(osc.NewMessage("/a"))
	bundle.Append(osc.NewMessage("/b"))
	stop := errors.New("stop")
	var d Decoder
	calls := 0
	err := d.Decode(marshal(t, bundle), func(*Message) error { calls++; return stop })
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("err = %v, calls = %d; want stop after 1 call", err, calls)
	}
}

func FuzzDecode(f *testing.F) {
	note := osc.NewMessage("/track/1/midi/note")
	note.Append(int32(1))
	note.Append("s")
	bundle := osc.NewBundle(time.Now())
	bundle.Append(note)
	for _, p := range []osc.Packet{note, bundle} {
		b, _ := p.MarshalBinary()
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		var d Decoder
		_ = d.Decode(b, func(*Message) error { return nil })
	})
}

func BenchmarkDecodeBundle(b *testing.B) {
	bundle := osc.NewBundle(time.Now())
	for i := range 1000 {
		m := osc.NewMessage("/track/1/midi/note")
		for _, v := range []int32{0, int32(i * 500), 60, 500, 450, 100} {
			m.Append(v)
		}
		bundle.Append(m)
	}
	p, _ := bundle.MarshalBinary()
	var d Decoder
	b.SetBytes(int64(len(p)))
	b.ReportAllocs()
	for b.Loop() {
		_ = d.Decode(p, func(*Message) error { return nil })
	}
}
