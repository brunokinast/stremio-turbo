package utHolepunch

import (
	"bytes"
	"net/netip"
	"testing"

	qt "github.com/go-quicktest/qt"
)

var exampleMsgs = []Msg{
	{
		MsgType:  Rendezvous,
		AddrPort: netip.MustParseAddrPort("[1234::1]:42069"),
		ErrCode:  16777216,
	},
	{
		MsgType:  Connect,
		AddrPort: netip.MustParseAddrPort("1.2.3.4:42069"),
		ErrCode:  16777216,
	},
}

func TestUnmarshalMsg(t *testing.T) {
	for _, m := range exampleMsgs {
		b, err := m.MarshalBinary()
		qt.Assert(t, qt.IsNil(err))
		expectedLen := 24
		if m.AddrPort.Addr().Is4() {
			expectedLen = 12
		}
		qt.Check(t, qt.HasLen(b, expectedLen))
		var um Msg
		err = um.UnmarshalBinary(b)
		qt.Assert(t, qt.IsNil(err))
		qt.Check(t, qt.Equals(um, m))
	}
}

// libtorrent leaves the error code out of rendezvous and connect messages.
func TestUnmarshalLibtorrentMsg(t *testing.T) {
	for _, c := range []struct {
		b    []byte
		want Msg
	}{
		{[]byte{1, 0, 1, 2, 3, 4, 0xa4, 0x55}, Msg{MsgType: Connect, AddrPort: netip.MustParseAddrPort("1.2.3.4:42069")}},
		{append(append([]byte{0, 1}, netip.MustParseAddr("1234::1").AsSlice()...), 0xa4, 0x55),
			Msg{MsgType: Rendezvous, AddrPort: netip.MustParseAddrPort("[1234::1]:42069")}},
		{[]byte{2, 0, 1, 2, 3, 4, 0xa4, 0x55, 0, 0, 0, 3}, Msg{MsgType: Error, AddrPort: netip.MustParseAddrPort("1.2.3.4:42069"), ErrCode: 3}},
	} {
		var m Msg
		qt.Assert(t, qt.IsNil(m.UnmarshalBinary(c.b)))
		qt.Check(t, qt.Equals(m, c.want))
	}
	for _, b := range [][]byte{{1}, {1, 0, 1, 2, 3, 4, 0xa4}, {1, 1, 1, 2, 3, 4, 0xa4, 0x55}, {1, 0, 1, 2, 3, 4, 0xa4, 0x55, 9}} {
		var m Msg
		qt.Check(t, qt.IsNotNil(m.UnmarshalBinary(b)), qt.Commentf("%x", b))
	}
}

func FuzzMsg(f *testing.F) {
	for _, m := range exampleMsgs {
		emb, err := m.MarshalBinary()
		if err != nil {
			f.Fatal(err)
		}
		f.Add(emb)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		var m Msg
		err := m.UnmarshalBinary(b)
		if err != nil {
			t.Skip(err)
		}
		mb, err := m.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		// The error code is written always, read when present: 8 and 20 bytes are IPv4 and IPv6
		// messages without it.
		if len(b) == 8 || len(b) == 20 {
			b = append(b, 0, 0, 0, 0)
		}
		if !bytes.Equal(b, mb) {
			t.Fatalf("remarshalled %x as %x", b, mb)
		}
	})
}
