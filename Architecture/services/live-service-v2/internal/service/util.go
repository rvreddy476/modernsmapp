package service

import (
	"bytes"
	"io"
)

// jsonReader is a tiny helper so handlers don't need to import bytes.
func jsonReader(b []byte) io.Reader { return bytes.NewReader(b) }
