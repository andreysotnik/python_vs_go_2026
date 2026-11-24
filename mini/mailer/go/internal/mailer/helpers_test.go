package mailer

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/quotedprintable"
)

func newQPReader(b []byte) io.Reader { return quotedprintable.NewReader(bytes.NewReader(b)) }

func newB64Reader(r io.Reader) io.Reader { return base64.NewDecoder(base64.StdEncoding, r) }
