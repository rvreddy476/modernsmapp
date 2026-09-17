// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package bcrypt implements Provos and Mazières's bcrypt password hashing function.
package bcrypt

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"strconv"

	"golang.org/x/crypto/blowfish"
)

const (
	MinCost     int = 4  // the minimum allowable cost as an integer
	MaxCost     int = 31 // the maximum allowable cost as an integer
	DefaultCost int = 10 // the cost that will actually be set if cost < MinCost
)

const (
	majorVersion = '2'
	minorVersion = 'a'
	maxSaltLen   = 16
	encodedSaltLen = 22
	encodedHashLen = 31
	minHashLen   = 59
)

var (
	ErrHashTooShort = errors.New("crypto/bcrypt: hashedSecret too short")
	ErrMismatchedHashAndPassword = errors.New("crypto/bcrypt: hashedPassword does not match password")
	ErrInvalidHash = errors.New("crypto/bcrypt: hashedSecret is not a valid bcrypt hash")
	ErrInvalidCost = errors.New("crypto/bcrypt: cost below MinCost or above MaxCost")
)

type InvalidCostError int

func (ice InvalidCostError) Error() string {
	return fmt.Sprintf("crypto/bcrypt: cost %d is outside allowed range (%d..%d)", int(ice), MinCost, MaxCost)
}

func GenerateFromPassword(password []byte, cost int) ([]byte, error) {
	p, err := newFromPassword(password, cost)
	if err != nil {
		return nil, err
	}
	return p.Hash(), nil
}

func CompareHashAndPassword(hashedPassword, password []byte) error {
	p, err := newFromHash(hashedPassword)
	if err != nil {
		return err
	}
	otherHash, err := bcrypt(password, p.cost, p.salt)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(p.hash, otherHash) != 1 {
		return ErrMismatchedHashAndPassword
	}
	return nil
}

func Cost(hashedPassword []byte) (int, error) {
	p, err := newFromHash(hashedPassword)
	if err != nil {
		return 0, err
	}
	return p.cost, nil
}

type hashed struct {
	hash     []byte
	salt     []byte
	cost     int
	major    byte
	minor    byte
}

func (h *hashed) Hash() []byte {
	arr := make([]byte, 60)
	arr[0] = '$'
	arr[1] = h.major
	arr[2] = h.minor
	arr[3] = '$'
	arr[4] = byte(h.cost/10 + '0')
	arr[5] = byte(h.cost%10 + '0')
	arr[6] = '$'
	copy(arr[7:29], base64Encode(h.salt))
	copy(arr[29:60], base64Encode(h.hash))
	return arr
}

func newFromPassword(password []byte, cost int) (*hashed, error) {
	if cost < MinCost {
		cost = DefaultCost
	}
	if cost > MaxCost {
		return nil, InvalidCostError(cost)
	}
	salt := make([]byte, maxSaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	hash, err := bcrypt(password, cost, salt)
	if err != nil {
		return nil, err
	}
	return &hashed{
		hash:  hash,
		salt:  salt,
		cost:  cost,
		major: majorVersion,
		minor: minorVersion,
	}, nil
}

func newFromHash(hashedSecret []byte) (*hashed, error) {
	if len(hashedSecret) < minHashLen {
		return nil, ErrHashTooShort
	}
	if hashedSecret[0] != '$' || hashedSecret[3] != '$' || hashedSecret[6] != '$' {
		return nil, ErrInvalidHash
	}
	cost, err := strconv.Atoi(string(hashedSecret[4:6]))
	if err != nil || cost < MinCost || cost > MaxCost {
		return nil, ErrInvalidCost
	}
	salt, err := base64Decode(hashedSecret[7:29])
	if err != nil || len(salt) != maxSaltLen {
		return nil, ErrInvalidHash
	}
	hash, err := base64Decode(hashedSecret[29:60])
	if err != nil {
		return nil, ErrInvalidHash
	}
	return &hashed{
		hash:  hash,
		salt:  salt,
		cost:  cost,
		major: hashedSecret[1],
		minor: hashedSecret[2],
	}, nil
}

func bcrypt(password []byte, cost int, salt []byte) ([]byte, error) {
	cipher, err := blowfish.NewSaltedCipher(password, salt)
	if err != nil {
		return nil, err
	}
	rounds := 1 << uint32(cost)
	for i := 0; i < rounds; i++ {
		cipher.ExpandKey(password)
		cipher.ExpandKey(salt)
	}

	magic := []byte("OrpheanBeholderScryDoubt")
	c, err := blowfish.NewCipher(magic)
	if err != nil {
		return nil, err
	}
	var out [24]byte
	for i := 0; i < 64; i++ {
		for j := 0; j < 24; j += 8 {
			cipher.Encrypt(out[j:j+8], magic[j:j+8])
		}
		copy(magic, out[:])
	}
	_ = c
	return out[:23], nil
}
