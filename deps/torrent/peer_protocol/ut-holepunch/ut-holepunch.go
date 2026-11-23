package utHolepunch

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/netip"
)

const ExtensionName = "ut_holepunch"

type (
	Msg struct {
		MsgType  MsgType
		AddrPort netip.AddrPort
		ErrCode  ErrCode
	}
	MsgType  byte
	AddrType byte
)

const (
	Rendezvous MsgType = iota
	Connect
	Error
)

func (me MsgType) String() string {
	switch me {
	case Rendezvous:
		return "rendezvous"
	case Connect:
		return "connect"
	case Error:
		return "error"
	default:
		return fmt.Sprintf("unknown %d", me)
	}
}

const (
	Ipv4 AddrType = iota
	Ipv6 AddrType = iota
)

// UnmarshalBinary accepts the error code as optional: BEP 55 carries it in error messages, and
// libtorrent (qBittorrent, Deluge) sends rendezvous and connect messages without it
// (bt_peer_connection.cpp, write_holepunch_msg): 8 bytes for IPv4, 20 for IPv6.
func (m *Msg) UnmarshalBinary(b []byte) error {
	if len(b) < 2 {
		return fmt.Errorf("buffer too small to be valid")
	}
	m.MsgType = MsgType(b[0])
	addrType := AddrType(b[1])
	b = b[2:]
	var addrLen int
	switch addrType {
	case Ipv4:
		addrLen = 4
	case Ipv6:
		addrLen = 16
	default:
		return fmt.Errorf("unhandled addr type value %v", addrType)
	}
	if len(b) < addrLen+2 {
		return fmt.Errorf("not enough bytes")
	}
	addr, _ := netip.AddrFromSlice(b[:addrLen])
	b = b[addrLen:]
	port := binary.BigEndian.Uint16(b[:2])
	b = b[2:]
	m.AddrPort = netip.AddrPortFrom(addr, port)
	m.ErrCode = 0
	switch len(b) {
	case 0:
	case 4:
		m.ErrCode = ErrCode(binary.BigEndian.Uint32(b))
	default:
		return fmt.Errorf("%v trailing unused bytes", len(b))
	}
	return nil
}

// MarshalBinary always writes the error code, which peers that require it (this package before
// UnmarshalBinary accepted it as optional) expect, and libtorrent ignores.
func (m *Msg) MarshalBinary() (_ []byte, err error) {
	var buf bytes.Buffer
	buf.Grow(24)
	buf.WriteByte(byte(m.MsgType))
	addr := m.AddrPort.Addr()
	switch {
	case addr.Is4():
		buf.WriteByte(byte(Ipv4))
	case addr.Is6():
		buf.WriteByte(byte(Ipv6))
	default:
		err = fmt.Errorf("unhandled addr type: %v", addr)
		return
	}
	buf.Write(addr.AsSlice())
	binary.Write(&buf, binary.BigEndian, m.AddrPort.Port())
	binary.Write(&buf, binary.BigEndian, m.ErrCode)
	return buf.Bytes(), nil
}
