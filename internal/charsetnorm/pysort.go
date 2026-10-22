package charsetnorm

// pyListSort is CPython 3.13's list.sort() (timsort, listobject.c) for a
// list without key function, driven only by the "<" predicate. Matching the
// exact algorithm matters because CharsetMatch.__lt__ is not a strict weak
// ordering: the result depends on which comparisons are made.
func pyListSort[T any](a []T, lt func(x, y T) bool) {
	n := len(a)
	if n < 2 {
		return
	}
	ms := &mergeState[T]{a: a, lt: lt, minGallop: minGallop, listLen: n}
	minrun := computeMinrun(n)
	lo := 0
	remaining := n
	for remaining > 0 {
		r := ms.countRun(lo, remaining)
		if r < minrun {
			force := minrun
			if remaining <= minrun {
				force = remaining
			}
			ms.binarySort(lo, force, r)
			r = force
		}
		ms.foundNewRun(r)
		ms.pending = append(ms.pending, run{base: lo, len: r})
		lo += r
		remaining -= r
	}
	ms.forceCollapse()
}

const (
	minGallop = 7
	maxMinrun = 64
)

type run struct {
	base, len int
	power     int
}

type mergeState[T any] struct {
	a         []T
	lt        func(x, y T) bool
	minGallop int
	pending   []run
	listLen   int
	tmp       []T
}

func computeMinrun(n int) int {
	r := 0
	for n >= maxMinrun {
		r |= n & 1
		n >>= 1
	}
	return n + r
}

func reverseSlice[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// binarySort sorts a[lo:lo+n] given that a[lo:lo+ok] is already sorted.
func (ms *mergeState[T]) binarySort(lo, n, ok int) {
	a := ms.a[lo : lo+n]
	if ok == 0 {
		ok = 1
	}
	for ; ok < n; ok++ {
		l, r := 0, ok
		pivot := a[ok]
		for l < r {
			m := (l + r) >> 1
			if ms.lt(pivot, a[m]) {
				r = m
			} else {
				l = m + 1
			}
		}
		copy(a[l+1:ok+1], a[l:ok])
		a[l] = pivot
	}
}

// countRun is count_run (3.13 version with equal-element handling).
func (ms *mergeState[T]) countRun(lo, nremaining int) int {
	a := ms.a[lo:]
	lt := ms.lt
	n := 1
	for ; n < nremaining; n++ {
		if lt(a[n], a[n-1]) {
			break
		}
	}
	if n == nremaining {
		return n
	}
	if n > 1 {
		if lt(a[0], a[n-1]) {
			return n
		}
		reverseSlice(a[:n])
	}
	n++
	neq := 0
	reverseLastNeq := func() {
		if neq > 0 {
			neq++
			reverseSlice(a[n-neq : n])
			neq = 0
		}
	}
	for ; n < nremaining; n++ {
		if lt(a[n], a[n-1]) {
			reverseLastNeq()
		} else {
			if lt(a[n-1], a[n]) {
				break
			}
			neq++
		}
	}
	reverseLastNeq()
	reverseSlice(a[:n])
	for ; n < nremaining; n++ {
		if lt(a[n], a[n-1]) {
			break
		}
	}
	return n
}

func (ms *mergeState[T]) gallopLeft(key T, a []T, hint int) int {
	n := len(a)
	lt := ms.lt
	lastofs, ofs := 0, 1
	if lt(a[hint], key) {
		maxofs := n - hint
		for ofs < maxofs {
			if lt(a[hint+ofs], key) {
				lastofs = ofs
				ofs = ofs<<1 + 1
			} else {
				break
			}
		}
		if ofs > maxofs {
			ofs = maxofs
		}
		lastofs += hint
		ofs += hint
	} else {
		maxofs := hint + 1
		for ofs < maxofs {
			if lt(a[hint-ofs], key) {
				break
			}
			lastofs = ofs
			ofs = ofs<<1 + 1
		}
		if ofs > maxofs {
			ofs = maxofs
		}
		k := lastofs
		lastofs = hint - ofs
		ofs = hint - k
	}
	lastofs++
	for lastofs < ofs {
		m := lastofs + ((ofs - lastofs) >> 1)
		if lt(a[m], key) {
			lastofs = m + 1
		} else {
			ofs = m
		}
	}
	return ofs
}

func (ms *mergeState[T]) gallopRight(key T, a []T, hint int) int {
	n := len(a)
	lt := ms.lt
	lastofs, ofs := 0, 1
	if lt(key, a[hint]) {
		maxofs := hint + 1
		for ofs < maxofs {
			if lt(key, a[hint-ofs]) {
				lastofs = ofs
				ofs = ofs<<1 + 1
			} else {
				break
			}
		}
		if ofs > maxofs {
			ofs = maxofs
		}
		k := lastofs
		lastofs = hint - ofs
		ofs = hint - k
	} else {
		maxofs := n - hint
		for ofs < maxofs {
			if lt(key, a[hint+ofs]) {
				break
			}
			lastofs = ofs
			ofs = ofs<<1 + 1
		}
		if ofs > maxofs {
			ofs = maxofs
		}
		lastofs += hint
		ofs += hint
	}
	lastofs++
	for lastofs < ofs {
		m := lastofs + ((ofs - lastofs) >> 1)
		if lt(key, a[m]) {
			ofs = m
		} else {
			lastofs = m + 1
		}
	}
	return ofs
}

// mergeLo merges a[pa:pa+na] and a[pb:pb+nb] (pb == pa+na), na <= nb.
func (ms *mergeState[T]) mergeLo(pa, na, pb, nb int) {
	arr := ms.a
	lt := ms.lt
	tmp := append(ms.tmp[:0], arr[pa:pa+na]...)
	ms.tmp = tmp
	dest := pa
	ia := 0 // index into tmp
	ib := pb
	arr[dest] = arr[ib]
	dest++
	ib++
	nb--
	if nb == 0 {
		goto succeed
	}
	if na == 1 {
		goto copyB
	}
	{
		mg := ms.minGallop
		for {
			acount, bcount := 0, 0
			for {
				if lt(arr[ib], tmp[ia]) {
					arr[dest] = arr[ib]
					dest++
					ib++
					bcount++
					acount = 0
					nb--
					if nb == 0 {
						goto succeed
					}
					if bcount >= mg {
						break
					}
				} else {
					arr[dest] = tmp[ia]
					dest++
					ia++
					acount++
					bcount = 0
					na--
					if na == 1 {
						goto copyB
					}
					if acount >= mg {
						break
					}
				}
			}
			mg++
			for {
				if mg > 1 {
					mg--
				}
				ms.minGallop = mg
				k := ms.gallopRight(arr[ib], tmp[ia:ia+na], 0)
				acount = k
				if k > 0 {
					copy(arr[dest:dest+k], tmp[ia:ia+k])
					dest += k
					ia += k
					na -= k
					if na == 1 {
						goto copyB
					}
					if na == 0 {
						goto succeed
					}
				}
				arr[dest] = arr[ib]
				dest++
				ib++
				nb--
				if nb == 0 {
					goto succeed
				}
				k = ms.gallopLeft(tmp[ia], arr[ib:ib+nb], 0)
				bcount = k
				if k > 0 {
					copy(arr[dest:dest+k], arr[ib:ib+k])
					dest += k
					ib += k
					nb -= k
					if nb == 0 {
						goto succeed
					}
				}
				arr[dest] = tmp[ia]
				dest++
				ia++
				na--
				if na == 1 {
					goto copyB
				}
				if !(acount >= minGallop || bcount >= minGallop) {
					break
				}
			}
			mg++
			ms.minGallop = mg
		}
	}
succeed:
	if na > 0 {
		copy(arr[dest:dest+na], tmp[ia:ia+na])
	}
	return
copyB:
	copy(arr[dest:dest+nb], arr[ib:ib+nb])
	arr[dest+nb] = tmp[ia]
}

// mergeHi merges a[pa:pa+na] and a[pb:pb+nb] (pb == pa+na), na > nb.
func (ms *mergeState[T]) mergeHi(pa, na, pb, nb int) {
	arr := ms.a
	lt := ms.lt
	tmp := append(ms.tmp[:0], arr[pb:pb+nb]...)
	ms.tmp = tmp
	dest := pb + nb - 1
	ia := pa + na - 1 // last of a (in arr)
	ib := nb - 1      // last of b (in tmp)
	arr[dest] = arr[ia]
	dest--
	ia--
	na--
	if na == 0 {
		goto succeed
	}
	if nb == 1 {
		goto copyA
	}
	{
		mg := ms.minGallop
		for {
			acount, bcount := 0, 0
			for {
				if lt(tmp[ib], arr[ia]) {
					arr[dest] = arr[ia]
					dest--
					ia--
					acount++
					bcount = 0
					na--
					if na == 0 {
						goto succeed
					}
					if acount >= mg {
						break
					}
				} else {
					arr[dest] = tmp[ib]
					dest--
					ib--
					bcount++
					acount = 0
					nb--
					if nb == 1 {
						goto copyA
					}
					if bcount >= mg {
						break
					}
				}
			}
			mg++
			for {
				if mg > 1 {
					mg--
				}
				ms.minGallop = mg
				k := ms.gallopRight(tmp[ib], arr[pa:pa+na], na-1)
				k = na - k
				acount = k
				if k > 0 {
					dest -= k
					ia -= k
					copy(arr[dest+1:dest+1+k], arr[ia+1:ia+1+k])
					na -= k
					if na == 0 {
						goto succeed
					}
				}
				arr[dest] = tmp[ib]
				dest--
				ib--
				nb--
				if nb == 1 {
					goto copyA
				}
				k = ms.gallopLeft(arr[ia], tmp[:nb], nb-1)
				k = nb - k
				bcount = k
				if k > 0 {
					dest -= k
					ib -= k
					copy(arr[dest+1:dest+1+k], tmp[ib+1:ib+1+k])
					nb -= k
					if nb == 1 {
						goto copyA
					}
					if nb == 0 {
						goto succeed
					}
				}
				arr[dest] = arr[ia]
				dest--
				ia--
				na--
				if na == 0 {
					goto succeed
				}
				if !(acount >= minGallop || bcount >= minGallop) {
					break
				}
			}
			mg++
			ms.minGallop = mg
		}
	}
succeed:
	if nb > 0 {
		copy(arr[dest-(nb-1):dest+1], tmp[:nb])
	}
	return
copyA:
	dest -= na
	ia -= na
	copy(arr[dest+1:dest+1+na], arr[ia+1:ia+1+na])
	arr[dest] = tmp[ib]
}

func (ms *mergeState[T]) mergeAt(i int) {
	p := ms.pending
	pa, na := p[i].base, p[i].len
	pb, nb := p[i+1].base, p[i+1].len
	p[i].len = na + nb
	if i == len(p)-3 {
		p[i+1] = p[i+2]
	}
	ms.pending = p[:len(p)-1]
	k := ms.gallopRight(ms.a[pb], ms.a[pa:pa+na], 0)
	pa += k
	na -= k
	if na == 0 {
		return
	}
	nb = ms.gallopLeft(ms.a[pa+na-1], ms.a[pb:pb+nb], nb-1)
	if nb <= 0 {
		return
	}
	if na <= nb {
		ms.mergeLo(pa, na, pb, nb)
	} else {
		ms.mergeHi(pa, na, pb, nb)
	}
}

func powerloop(s1, n1, n2, n int) int {
	result := 0
	a := 2*s1 + n1
	b := a + n1 + n2
	for {
		result++
		if a >= n {
			a -= n
			b -= n
		} else if b >= n {
			break
		}
		a <<= 1
		b <<= 1
	}
	return result
}

func (ms *mergeState[T]) foundNewRun(n2 int) {
	if len(ms.pending) == 0 {
		return
	}
	last := ms.pending[len(ms.pending)-1]
	power := powerloop(last.base, last.len, n2, ms.listLen)
	for len(ms.pending) > 1 && ms.pending[len(ms.pending)-2].power > power {
		ms.mergeAt(len(ms.pending) - 2)
	}
	ms.pending[len(ms.pending)-1].power = power
}

func (ms *mergeState[T]) forceCollapse() {
	for len(ms.pending) > 1 {
		n := len(ms.pending) - 2
		if n > 0 && ms.pending[n-1].len < ms.pending[n+1].len {
			n--
		}
		ms.mergeAt(n)
	}
}
