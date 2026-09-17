// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bcrypt

import "encoding/base64"

const alphabet = "./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

var bcEncoding = base64.NewEncoding(alphabet)

func base64Encode(src []byte) []byte {
	n := bcEncoding.EncodedLen(len(src))
	dst := make([]byte, n)
	bcEncoding.Encode(dst, src)
	for dst[n-1] == '=' {
		n--
	}
	return dst[:n]
}

func base64Decode(src []byte) ([]byte, error) {
	numOfEquals := 0
	if n := len(src) % 4; n != 0 {
		numOfEquals = 4 - n
	}
	newSrc := make([]byte, len(src)+numOfEquals)
	copy(newSrc, src)
	for i := 0; i < numOfEquals; i++ {
		newSrc[len(src)+i] = '='
	}
	dst := make([]byte, bcEncoding.DecodedLen(len(newSrc)))
	n, err := bcEncoding.Decode(dst, newSrc)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}
