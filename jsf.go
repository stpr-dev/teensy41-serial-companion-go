package main

import "math/bits"

type JSF32 struct {
	a, b, c, d uint32
}

func NewJSF32(seed uint32) *JSF32 {
	r := &JSF32{0xf1ea5eed, seed, seed, seed}
	for range 20 {
		r.Next()
	}
	return r
}

func (r *JSF32) Next() uint32 {
	e := r.a - bits.RotateLeft32(r.b, 27)
	r.a = r.b ^ bits.RotateLeft32(r.c, 17)
	r.b = r.c + r.d
	r.c = r.d + e
	r.d = e + r.a
	return r.d
}
