package par2

import "encoding/binary"

// The arithmetic of PAR 2.0: the Galois field GF(2^16) of the polynomial 0x1100B, on which the
// recovery slices are sums of the input slices multiplied by constants, 16 bits at a time
// (little endian).

const (
	fieldPoly  = 0x1100B
	fieldLimit = 65535 // the multiplicative group: exponents are taken modulo it
)

var (
	fieldExp [fieldLimit]uint16
	fieldLog [fieldLimit + 1]uint16
)

func init() {
	value := 1
	for i := 0; i < fieldLimit; i++ {
		fieldExp[i] = uint16(value)
		fieldLog[value] = uint16(i)
		value <<= 1
		if value&0x10000 != 0 {
			value ^= fieldPoly
		}
	}
}

func gfMul(a, b uint16) uint16 {
	if a == 0 || b == 0 {
		return 0
	}
	return fieldExp[(int(fieldLog[a])+int(fieldLog[b]))%fieldLimit]
}

func gfInv(a uint16) uint16 {
	return fieldExp[(fieldLimit-int(fieldLog[a]))%fieldLimit]
}

// gfPow2 is 2 to the power n
func gfPow2(n int) uint16 {
	return fieldExp[n%fieldLimit]
}

// inputLogs are the logarithms of the constants of the first count input slices: the exponents
// coprime with 65535, from 1 up, as PAR 2.0 takes them.
func inputLogs(count int) []int {
	logs := make([]int, 0, count)
	for n := 1; len(logs) < count; n++ {
		if n%3 != 0 && n%5 != 0 && n%17 != 0 && n%257 != 0 {
			logs = append(logs, n)
		}
	}
	return logs
}

// coefficient is the constant of an input slice (by its logarithm) in the recovery slice of
// exponent: its constant to the power exponent.
func coefficient(inputLog int, exponent int) uint16 {
	return gfPow2(int((int64(inputLog) * int64(exponent)) % fieldLimit))
}

// mulTable multiplies 16-bit words by one constant, by their two bytes.
type mulTable struct {
	low, high [256]uint16
}

func newMulTable(c uint16) *mulTable {
	t := &mulTable{}
	for x := 0; x < 256; x++ {
		t.low[x] = gfMul(c, uint16(x))
		t.high[x] = gfMul(c, uint16(x)<<8)
	}
	return t
}

// mulAdd adds src multiplied by the constant of t to dst; both have the same even length.
func (t *mulTable) mulAdd(dst, src []byte) {
	n := len(src) &^ 7
	for i := 0; i < n; i += 8 {
		s := src[i : i+8 : i+8]
		d := dst[i : i+8 : i+8]
		v := binary.LittleEndian.Uint64(d)
		v ^= uint64(t.low[s[0]]^t.high[s[1]]) |
			uint64(t.low[s[2]]^t.high[s[3]])<<16 |
			uint64(t.low[s[4]]^t.high[s[5]])<<32 |
			uint64(t.low[s[6]]^t.high[s[7]])<<48
		binary.LittleEndian.PutUint64(d, v)
	}
	for i := n; i+1 < len(src); i += 2 {
		w := t.low[src[i]] ^ t.high[src[i+1]]
		dst[i] ^= byte(w)
		dst[i+1] ^= byte(w >> 8)
	}
}

// invert gives the inverse of the square matrix m, or false when it has none.
func invert(m [][]uint16) ([][]uint16, bool) {
	n := len(m)
	a := make([][]uint16, n)
	inv := make([][]uint16, n)
	for i := range m {
		a[i] = append([]uint16(nil), m[i]...)
		inv[i] = make([]uint16, n)
		inv[i][i] = 1
	}
	for col := 0; col < n; col++ {
		pivot := -1
		for row := col; row < n; row++ {
			if a[row][col] != 0 {
				pivot = row
				break
			}
		}
		if pivot < 0 {
			return nil, false
		}
		a[col], a[pivot] = a[pivot], a[col]
		inv[col], inv[pivot] = inv[pivot], inv[col]
		scale := gfInv(a[col][col])
		for j := 0; j < n; j++ {
			a[col][j] = gfMul(a[col][j], scale)
			inv[col][j] = gfMul(inv[col][j], scale)
		}
		for row := 0; row < n; row++ {
			if row == col || a[row][col] == 0 {
				continue
			}
			factor := a[row][col]
			for j := 0; j < n; j++ {
				a[row][j] ^= gfMul(factor, a[col][j])
				inv[row][j] ^= gfMul(factor, inv[col][j])
			}
		}
	}
	return inv, true
}
