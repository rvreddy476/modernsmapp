// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package blowfish implements Bruce Schneier's Blowfish encryption algorithm.
package blowfish

import (
	"strconv"
)

// A Cipher is an instance of Blowfish encryption using a particular key.
type Cipher struct {
	p              [18]uint32
	s0, s1, s2, s3 [256]uint32
}

type KeySizeError int

func (k KeySizeError) Error() string {
	return "crypto/blowfish: invalid key size " + strconv.Itoa(int(k))
}

// NewCipher creates and returns a new Cipher.
func NewCipher(key []byte) (*Cipher, error) {
	var result Cipher
	if err := initCipher(&result, key); err != nil {
		return nil, err
	}
	return &result, nil
}

// NewSaltedCipher creates a Blowfish cipher using a key and salt (used by bcrypt).
func NewSaltedCipher(key, salt []byte) (*Cipher, error) {
	if len(salt) == 0 {
		return NewCipher(key)
	}
	var result Cipher
	if err := initCipher(&result, key); err != nil {
		return nil, err
	}
	result.ExpandKeyWithSalt(key, salt)
	return &result, nil
}

func initCipher(c *Cipher, key []byte) error {
	k := len(key)
	if k < 1 || k > 56 {
		return KeySizeError(k)
	}
	copy(c.p[:], p[:])
	copy(c.s0[:], s0[:])
	copy(c.s1[:], s1[:])
	copy(c.s2[:], s2[:])
	copy(c.s3[:], s3[:])
	c.ExpandKey(key)
	return nil
}

// BlockSize returns the Blowfish block size, 8 bytes.
func (c *Cipher) BlockSize() int { return BlockSize }

// Encrypt encrypts the 8-byte buffer src using the key and stores the result in dst.
func (c *Cipher) Encrypt(dst, src []byte) {
	if len(src) < BlockSize {
		panic("crypto/blowfish: input not full block")
	}
	if len(dst) < BlockSize {
		panic("crypto/blowfish: output not full block")
	}
	l := uint32(src[0])<<24 | uint32(src[1])<<16 | uint32(src[2])<<8 | uint32(src[3])
	r := uint32(src[4])<<24 | uint32(src[5])<<16 | uint32(src[6])<<8 | uint32(src[7])
	l, r = encryptBlock(l, r, c)
	dst[0], dst[1], dst[2], dst[3] = byte(l>>24), byte(l>>16), byte(l>>8), byte(l)
	dst[4], dst[5], dst[6], dst[7] = byte(r>>24), byte(r>>16), byte(r>>8), byte(r)
}

// Decrypt decrypts the 8-byte buffer src using the key and stores the result in dst.
func (c *Cipher) Decrypt(dst, src []byte) {
	if len(src) < BlockSize {
		panic("crypto/blowfish: input not full block")
	}
	if len(dst) < BlockSize {
		panic("crypto/blowfish: output not full block")
	}
	l := uint32(src[0])<<24 | uint32(src[1])<<16 | uint32(src[2])<<8 | uint32(src[3])
	r := uint32(src[4])<<24 | uint32(src[5])<<16 | uint32(src[6])<<8 | uint32(src[7])
	l, r = decryptBlock(l, r, c)
	dst[0], dst[1], dst[2], dst[3] = byte(l>>24), byte(l>>16), byte(l>>8), byte(l)
	dst[4], dst[5], dst[6], dst[7] = byte(r>>24), byte(r>>16), byte(r>>8), byte(r)
}

func (c *Cipher) ExpandKey(key []byte) {
	j := 0
	for i := 0; i < 18; i++ {
		var d uint32
		for k := 0; k < 4; k++ {
			d = (d << 8) | uint32(key[j])
			j++
			if j >= len(key) {
				j = 0
			}
		}
		c.p[i] ^= d
	}

	var l, r uint32
	for i := 0; i < 18; i += 2 {
		l, r = encryptBlock(l, r, c)
		c.p[i], c.p[i+1] = l, r
	}

	for i := 0; i < 256; i += 2 {
		l, r = encryptBlock(l, r, c)
		c.s0[i], c.s0[i+1] = l, r
	}
	for i := 0; i < 256; i += 2 {
		l, r = encryptBlock(l, r, c)
		c.s1[i], c.s1[i+1] = l, r
	}
	for i := 0; i < 256; i += 2 {
		l, r = encryptBlock(l, r, c)
		c.s2[i], c.s2[i+1] = l, r
	}
	for i := 0; i < 256; i += 2 {
		l, r = encryptBlock(l, r, c)
		c.s3[i], c.s3[i+1] = l, r
	}
}

func (c *Cipher) ExpandKeyWithSalt(key, salt []byte) {
	j := 0
	for i := 0; i < 18; i++ {
		var d uint32
		for k := 0; k < 4; k++ {
			d = (d << 8) | uint32(key[j])
			j++
			if j >= len(key) {
				j = 0
			}
		}
		c.p[i] ^= d
	}

	j = 0
	var l, r uint32
	for i := 0; i < 18; i += 2 {
		l ^= uint32(salt[j])<<24 | uint32(salt[(j+1)%len(salt)])<<16 | uint32(salt[(j+2)%len(salt)])<<8 | uint32(salt[(j+3)%len(salt)])
		r ^= uint32(salt[(j+4)%len(salt)])<<24 | uint32(salt[(j+5)%len(salt)])<<16 | uint32(salt[(j+6)%len(salt)])<<8 | uint32(salt[(j+7)%len(salt)])
		j = (j + 8) % len(salt)
		l, r = encryptBlock(l, r, c)
		c.p[i], c.p[i+1] = l, r
	}

	for i := 0; i < 256; i += 2 {
		l ^= uint32(salt[j])<<24 | uint32(salt[(j+1)%len(salt)])<<16 | uint32(salt[(j+2)%len(salt)])<<8 | uint32(salt[(j+3)%len(salt)])
		r ^= uint32(salt[(j+4)%len(salt)])<<24 | uint32(salt[(j+5)%len(salt)])<<16 | uint32(salt[(j+6)%len(salt)])<<8 | uint32(salt[(j+7)%len(salt)])
		j = (j + 8) % len(salt)
		l, r = encryptBlock(l, r, c)
		c.s0[i], c.s0[i+1] = l, r
	}
	for i := 0; i < 256; i += 2 {
		l ^= uint32(salt[j])<<24 | uint32(salt[(j+1)%len(salt)])<<16 | uint32(salt[(j+2)%len(salt)])<<8 | uint32(salt[(j+3)%len(salt)])
		r ^= uint32(salt[(j+4)%len(salt)])<<24 | uint32(salt[(j+5)%len(salt)])<<16 | uint32(salt[(j+6)%len(salt)])<<8 | uint32(salt[(j+7)%len(salt)])
		j = (j + 8) % len(salt)
		l, r = encryptBlock(l, r, c)
		c.s1[i], c.s1[i+1] = l, r
	}
	for i := 0; i < 256; i += 2 {
		l ^= uint32(salt[j])<<24 | uint32(salt[(j+1)%len(salt)])<<16 | uint32(salt[(j+2)%len(salt)])<<8 | uint32(salt[(j+3)%len(salt)])
		r ^= uint32(salt[(j+4)%len(salt)])<<24 | uint32(salt[(j+5)%len(salt)])<<16 | uint32(salt[(j+6)%len(salt)])<<8 | uint32(salt[(j+7)%len(salt)])
		j = (j + 8) % len(salt)
		l, r = encryptBlock(l, r, c)
		c.s2[i], c.s2[i+1] = l, r
	}
	for i := 0; i < 256; i += 2 {
		l ^= uint32(salt[j])<<24 | uint32(salt[(j+1)%len(salt)])<<16 | uint32(salt[(j+2)%len(salt)])<<8 | uint32(salt[(j+3)%len(salt)])
		r ^= uint32(salt[(j+4)%len(salt)])<<24 | uint32(salt[(j+5)%len(salt)])<<16 | uint32(salt[(j+6)%len(salt)])<<8 | uint32(salt[(j+7)%len(salt)])
		j = (j + 8) % len(salt)
		l, r = encryptBlock(l, r, c)
		c.s3[i], c.s3[i+1] = l, r
	}
}
