package switchfs

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

// The LZ4 blocks of the compressed storage of an NCA (CompressionInfo, firmware 12 and later) are
// made by two compressors of the tools of Nintendo, told by the level of each block in its table:
//
//   - level 0, the fast compressor of LZ4 before 1.8.2: a table of 13 bits, the position right
//     after a match tried at once, and matches searched only up to 12 bytes before the end. Its
//     hash is of 5 bytes as built for 64 bits, and of 4 bytes as built for 32 bits: games use
//     either;
//   - levels 1 to 16, LZ4HC of LZ4 r1xx (before 1.7): a hash chain searched 1 << (level-1) times,
//     16 at level 5 and 32,768 at level 16, without the analysis of repeated patterns of 1.8, and
//     the second search of a match starting one byte after the first one.
//
// Both are written here after lz4.c and lz4hc.c of LZ4 (BSD 2-Clause License, Copyright Yann
// Collet), set as those tools set them: they make the same blocks again, byte for byte, so a
// block can be kept decompressed and compressed again when the NCA is rebuilt. Checked on every
// block of three updates and three base games (more than 300,000 blocks of levels 0, 5 and 16).

const (
	lz4MinMatch     = 4
	lz4MFLimit      = 12
	lz4LastLiterals = 5
	lz4MinLength    = lz4MFLimit + 1
	lz4MLBits       = 4
	lz4MLMask       = 15
	lz4RunMask      = 15
	lz4MaxDistance  = 65535
)

func lz4Read32(b []byte, i int) uint32 { return binary.LittleEndian.Uint32(b[i:]) }

// lz4Count is how many bytes from in equal those from match, up to limit; match is before in.
func lz4Count(src []byte, in, match, limit int) int {
	start := in
	for in+8 <= limit {
		if diff := binary.LittleEndian.Uint64(src[in:]) ^ binary.LittleEndian.Uint64(src[match:]); diff != 0 {
			return in - start + bits.TrailingZeros64(diff)>>3
		}
		in += 8
		match += 8
	}
	for in < limit && src[in] == src[match] {
		in++
		match++
	}
	return in - start
}

// lz4Literals appends a token with literals of length n and the literals, and gives the place of
// the token.
func lz4Literals(out []byte, literals []byte) ([]byte, int) {
	token := len(out)
	n := len(literals)
	if n >= lz4RunMask {
		out = append(out, lz4RunMask<<lz4MLBits)
		for l := n - lz4RunMask; ; l -= 255 {
			if l < 255 {
				out = append(out, byte(l))
				break
			}
			out = append(out, 255)
		}
	} else {
		out = append(out, byte(n<<lz4MLBits))
	}
	return append(out, literals...), token
}

// lz4Decompress decompresses an LZ4 block into exactly size bytes.
func lz4Decompress(block []byte, size int) ([]byte, error) {
	bad := errors.New("damaged LZ4 block")
	out := make([]byte, 0, size)
	i := 0
	for i < len(block) {
		token := block[i]
		i++
		lit := int(token >> 4)
		if lit == 15 {
			for {
				if i >= len(block) {
					return nil, bad
				}
				b := block[i]
				i++
				lit += int(b)
				if b != 255 {
					break
				}
			}
		}
		if i+lit > len(block) || len(out)+lit > size {
			return nil, bad
		}
		out = append(out, block[i:i+lit]...)
		i += lit
		if i == len(block) {
			break
		}
		if i+2 > len(block) {
			return nil, bad
		}
		offset := int(block[i]) | int(block[i+1])<<8
		i += 2
		ml := int(token&15) + lz4MinMatch
		if token&15 == 15 {
			for {
				if i >= len(block) {
					return nil, bad
				}
				b := block[i]
				i++
				ml += int(b)
				if b != 255 {
					break
				}
			}
		}
		if offset == 0 || offset > len(out) || len(out)+ml > size {
			return nil, bad
		}
		start := len(out) - offset
		for k := 0; k < ml; k++ {
			out = append(out, out[start+k])
		}
	}
	if len(out) != size {
		return nil, bad
	}
	return out, nil
}

// lz4Level0Hash4 is the level an NCZSECT3 tells for a block of level 0 made by the fast compressor
// as built for 32 bits, with the hash of 4 bytes. The levels of LZ4HC r1xx go up to 16.
const lz4Level0Hash4 int8 = 64

// nintendoLz4 compresses src as the block of the given level was made, and tells whether the
// level is one of those known.
func nintendoLz4(src []byte, level int8) ([]byte, bool) {
	switch {
	case level == 0:
		return lz4CompressFast(src, true), true
	case level == lz4Level0Hash4:
		return lz4CompressFast(src, false), true
	case level >= 1 && level <= 16:
		return lz4CompressHC(src, 1<<(level-1)), true
	}
	return nil, false
}

// nintendoLz4Levels are the levels nintendoLz4 may make a block of the given level of an NCA at,
// in the order to try them.
func nintendoLz4Levels(level int8) []int8 {
	if level == 0 {
		return []int8{0, lz4Level0Hash4}
	}
	return []int8{level}
}

// lz4FastHash is the hash of the fast compressor on a table of 13 bits: of 5 bytes, or of 4 when
// wide is false.
func lz4FastHash(src []byte, i int, wide bool) uint32 {
	if !wide {
		return (lz4Read32(src, i) * 2654435761) >> (32 - 13)
	}
	var v uint64
	if i+8 <= len(src) {
		v = binary.LittleEndian.Uint64(src[i:])
	} else {
		var tail [8]byte
		copy(tail[:], src[i:])
		v = binary.LittleEndian.Uint64(tail[:])
	}
	return uint32(((v << 24) * 889523592379) >> (64 - 13))
}

// lz4CompressFast is LZ4_compress_generic of LZ4 before 1.8.2, with no dictionary, with the hash
// of 5 bytes when wide is set.
func lz4CompressFast(src []byte, wide bool) []byte {
	out := make([]byte, 0, len(src)+len(src)/255+16)
	var table [1 << 13]int32
	n := len(src)
	anchor, ip := 0, 0
	// matches are searched up to 12 bytes before the end (one byte less than LZ4 1.8.2 and later)
	mflimit := n - lz4MFLimit
	matchLimit := n - lz4LastLiterals
	if n < lz4MinLength {
		out, _ = lz4Literals(out, src)
		return out
	}
	table[lz4FastHash(src, ip, wide)] = int32(ip)
	ip++
	forwardH := lz4FastHash(src, ip, wide)
	for {
		var match int
		forwardIp := ip
		step := 1
		searchMatchNb := 1 << 6
		for {
			h := forwardH
			current := forwardIp
			matchIndex := int(table[h])
			ip = forwardIp
			forwardIp += step
			step = searchMatchNb >> 6
			searchMatchNb++
			if forwardIp > mflimit {
				out, _ = lz4Literals(out, src[anchor:])
				return out
			}
			match = matchIndex
			forwardH = lz4FastHash(src, forwardIp, wide)
			table[h] = int32(current)
			if matchIndex+lz4MaxDistance < current {
				continue
			}
			if match < ip && lz4Read32(src, match) == lz4Read32(src, ip) {
				break
			}
		}
		// catch up
		for ip > anchor && match > 0 && src[ip-1] == src[match-1] {
			ip--
			match--
		}
		var token int
		out, token = lz4Literals(out, src[anchor:ip])
		for {
			offset := ip - match
			out = append(out, byte(offset), byte(offset>>8))
			matchCode := lz4Count(src, ip+lz4MinMatch, match+lz4MinMatch, matchLimit)
			ip += matchCode + lz4MinMatch
			if matchCode >= lz4MLMask {
				out[token] += lz4MLMask
				matchCode -= lz4MLMask
				for ; matchCode >= 255; matchCode -= 255 {
					out = append(out, 255)
				}
				out = append(out, byte(matchCode))
			} else {
				out[token] += byte(matchCode)
			}
			anchor = ip
			if ip > mflimit {
				out, _ = lz4Literals(out, src[anchor:])
				return out
			}
			table[lz4FastHash(src, ip-2, wide)] = int32(ip - 2)
			// the position right after the match
			h := lz4FastHash(src, ip, wide)
			matchIndex := int(table[h])
			table[h] = int32(ip)
			if matchIndex+lz4MaxDistance >= ip && matchIndex < ip && lz4Read32(src, matchIndex) == lz4Read32(src, ip) {
				match = matchIndex
				token = len(out)
				out = append(out, 0)
				continue
			}
			break
		}
		ip++
		forwardH = lz4FastHash(src, ip, wide)
	}
}

// the hash chain of LZ4HC: indices start at 64 KiB, as LZ4HC_init sets them
const (
	lz4hcHashLog   = 15
	lz4hcMaxD      = 1 << 16
	lz4hcOptimalML = (lz4MLMask - 1) + lz4MinMatch
	lz4hcPrefix    = 64 << 10
)

type lz4hcState struct {
	src          []byte
	hashTable    [1 << lz4hcHashLog]uint32
	chain        [lz4hcMaxD]uint16
	nextToUpdate uint32
	attempts     int
}

func (s *lz4hcState) hash(idx uint32) uint32 {
	return (lz4Read32(s.src, int(idx-lz4hcPrefix)) * 2654435761) >> (32 - lz4hcHashLog)
}

func (s *lz4hcState) insert(target uint32) {
	for idx := s.nextToUpdate; idx < target; idx++ {
		h := s.hash(idx)
		delta := idx - s.hashTable[h]
		if delta > lz4MaxDistance {
			delta = lz4MaxDistance
		}
		s.chain[idx&(lz4hcMaxD-1)] = uint16(delta)
		s.hashTable[h] = idx
	}
	s.nextToUpdate = target
}

// widerMatch is LZ4HC_InsertAndGetWiderMatch: from ip, a match longer than longest that may
// start back down to iLow. It gives its length, where it is and where it starts.
func (s *lz4hcState) widerMatch(ip, iLow, iHigh, longest int) (int, int, int) {
	src := s.src
	ipIdx := uint32(ip + lz4hcPrefix)
	lowLimit := uint32(lz4hcPrefix)
	if lz4hcPrefix+(64<<10) <= ipIdx {
		lowLimit = ipIdx - lz4MaxDistance
	}
	delta := ip - iLow
	s.insert(ipIdx)
	matchIndex := s.hashTable[s.hash(ipIdx)]
	matchPos, startPos := -1, -1
	// what does not change while the chain is searched, at 32,768 times a search at level 16
	head, last := lz4Read32(src, ip), src[iLow+longest]
	chain := &s.chain
	for nb := s.attempts; matchIndex >= lowLimit && nb > 0; nb-- {
		m := int(matchIndex) - lz4hcPrefix
		if last == src[m-delta+longest] && lz4Read32(src, m) == head {
			mlt := lz4MinMatch + lz4Count(src, ip+lz4MinMatch, m+lz4MinMatch, iHigh)
			back := 0
			for ip+back > iLow && m+back > 0 && src[ip+back-1] == src[m+back-1] {
				back--
			}
			mlt -= back
			if mlt > longest {
				longest = mlt
				matchPos = m + back
				startPos = ip + back
				last = src[iLow+longest]
			}
		}
		matchIndex -= uint32(chain[uint16(matchIndex)])
	}
	return longest, matchPos, startPos
}

// lz4CompressHC is LZ4HC_compress_generic of LZ4 r1xx, whose levels are all of a hash chain,
// searching it attempts times.
func lz4CompressHC(src []byte, attempts int) []byte {
	s := &lz4hcState{src: src, nextToUpdate: lz4hcPrefix, attempts: attempts}
	for i := range s.chain {
		s.chain[i] = 0xFFFF
	}
	out := make([]byte, 0, len(src)+len(src)/255+16)
	n := len(src)
	ip, anchor := 0, 0
	mflimit := n - lz4MFLimit
	matchLimit := n - lz4LastLiterals
	encode := func(ml, ref int) {
		var token int
		out, token = lz4Literals(out, src[anchor:ip])
		offset := ip - ref
		out = append(out, byte(offset), byte(offset>>8))
		l := ml - lz4MinMatch
		if l >= lz4MLMask {
			out[token] += lz4MLMask
			l -= lz4MLMask
			for ; l >= 510; l -= 510 {
				out = append(out, 255, 255)
			}
			if l >= 255 {
				l -= 255
				out = append(out, 255)
			}
			out = append(out, byte(l))
		} else {
			out[token] += byte(l)
		}
		ip += ml
		anchor = ip
	}
	if n >= lz4MinLength {
		var ml, ml2, ml3, ml0 int
		var ref, start2, ref2, start3, ref3, start0, ref0 int
		for ip < mflimit {
			ml, ref, _ = s.widerMatch(ip, ip, matchLimit, lz4MinMatch-1)
			if ml < lz4MinMatch {
				ip++
				continue
			}
			start0, ref0, ml0 = ip, ref, ml
		search2:
			if ip+ml < mflimit {
				// r1xx: the wider match may start one byte after ip at the earliest
				ml2, ref2, start2 = s.widerMatch(ip+ml-2, ip+1, matchLimit, ml)
			} else {
				ml2 = ml
			}
			if ml2 == ml {
				encode(ml, ref)
				continue
			}
			if start0 < ip && start2 < ip+ml0 {
				ip, ref, ml = start0, ref0, ml0
			}
			if start2-ip < 3 {
				ml, ip, ref = ml2, start2, ref2
				goto search2
			}
		search3:
			if start2-ip < lz4hcOptimalML {
				newML := min(ml, lz4hcOptimalML)
				if ip+newML > start2+ml2-lz4MinMatch {
					newML = start2 - ip + ml2 - lz4MinMatch
				}
				if correction := newML - (start2 - ip); correction > 0 {
					start2 += correction
					ref2 += correction
					ml2 -= correction
				}
			}
			if start2+ml2 < mflimit {
				ml3, ref3, start3 = s.widerMatch(start2+ml2-3, start2, matchLimit, ml2)
			} else {
				ml3 = ml2
			}
			if ml3 == ml2 {
				if start2 < ip+ml {
					ml = start2 - ip
				}
				encode(ml, ref)
				ip = start2
				encode(ml2, ref2)
				continue
			}
			if start3 < ip+ml+3 {
				if start3 >= ip+ml {
					if start2 < ip+ml {
						correction := ip + ml - start2
						start2 += correction
						ref2 += correction
						ml2 -= correction
						if ml2 < lz4MinMatch {
							start2, ref2, ml2 = start3, ref3, ml3
						}
					}
					encode(ml, ref)
					ip, ref, ml = start3, ref3, ml3
					start0, ref0, ml0 = start2, ref2, ml2
					goto search2
				}
				start2, ref2, ml2 = start3, ref3, ml3
				goto search3
			}
			if start2 < ip+ml {
				if start2-ip < lz4MLMask {
					ml = min(ml, lz4hcOptimalML)
					if ip+ml > start2+ml2-lz4MinMatch {
						ml = start2 - ip + ml2 - lz4MinMatch
					}
					if correction := ml - (start2 - ip); correction > 0 {
						start2 += correction
						ref2 += correction
						ml2 -= correction
					}
				} else {
					ml = start2 - ip
				}
			}
			encode(ml, ref)
			ip, ref, ml = start2, ref2, ml2
			start2, ref2, ml2 = start3, ref3, ml3
			goto search3
		}
	}
	out, _ = lz4Literals(out, src[anchor:])
	return out
}
