package purego

import (
	"testing"

	"github.com/go-quicktest/qt"
)

// A connection whose reorder buffer holds the packets at the given distances past ackNr+1.
func connWithReordered(ackNr uint16, offsets ...uint32) *Conn {
	c := &Conn{ackNr: ackNr}
	c.inbuf.init()
	for _, o := range offsets {
		seq := uint32(ackNr) + 1 + o
		c.inbuf.ensureSize(seq+1, o+1)
		c.inbuf.put(seq, &inboundPacket{})
		if c.reorderCount == 0 || wrappingCompareLess(uint32(c.reorderLast), seq&seqNrMask, seqNrMask) {
			c.reorderLast = uint16(seq)
		}
		c.reorderCount++
	}
	return c
}

func maskBits(mask []byte) (bits []int) {
	for i := range len(mask) * 8 {
		if mask[i/8]&(1<<(i%8)) != 0 {
			bits = append(bits, i)
		}
	}
	return
}

func TestSelectiveAckMaskCoversReorderBuffer(t *testing.T) {
	// Packets 6, 41 and 901 past the hole; mask bit i is packet ackNr+2+i. Starting near the
	// sequence number wrap checks the mask follows wrapped numbers.
	c := connWithReordered(0xfff0, 6, 41, 901)
	var mask [maxSelectiveAckBytes]byte
	ln := c.selectiveAckMask(&mask)
	qt.Check(t, qt.Equals(ln, 116)) // bit 900 is in the 29th 32-bit word
	qt.Check(t, qt.DeepEquals(maskBits(mask[:ln]), []int{5, 40, 900}))
	qt.Check(t, qt.DeepEquals(maskBits(mask[ln:]), []int(nil)))
}

func TestSelectiveAckMaskShortWhenHolesAreNear(t *testing.T) {
	c := connWithReordered(100, 1, 30)
	var mask [maxSelectiveAckBytes]byte
	ln := c.selectiveAckMask(&mask)
	qt.Check(t, qt.Equals(ln, 4))
	qt.Check(t, qt.DeepEquals(maskBits(mask[:ln]), []int{0, 29}))
}

func TestSelectiveAckMaskFarthestSlot(t *testing.T) {
	// The farthest packet the reorder buffer accepts (seqNr 1023 past ackNr+1) fits the mask.
	c := connWithReordered(7, reorderBufferMaxSize-1)
	var mask [maxSelectiveAckBytes]byte
	ln := c.selectiveAckMask(&mask)
	qt.Check(t, qt.Equals(ln, maxSelectiveAckBytes))
	qt.Check(t, qt.DeepEquals(maskBits(mask[:ln]), []int{reorderBufferMaxSize - 2}))
}

// The mask can describe the whole 2016-packet reorder buffer in 252 bytes, the most the one-byte
// extension length allows in multiples of 4.
func TestSelectiveAckMaskMaxLength(t *testing.T) {
	qt.Assert(t, qt.Equals(maxSelectiveAckBytes, 252))
	c := connWithReordered(65000, 1500) // wraps past 0xffff
	var mask [maxSelectiveAckBytes]byte
	ln := c.selectiveAckMask(&mask)
	qt.Check(t, qt.Equals(ln, 188)) // bit 1499 is in byte 187: 47 words
	qt.Check(t, qt.DeepEquals(maskBits(mask[:ln]), []int{1499}))
}
