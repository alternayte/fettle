package vault

import (
	"bufio"
	"context"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
)

// SegmentSize is the plaintext size of one encrypted segment of a workspace archive (§10.3).
const SegmentSize = 64 * 1024

const streamMagic = "FTW1"

// segment AAD binds each segment to the archive, its index and whether it is the last one,
// so segments cannot be reordered, dropped or truncated.
func segAAD(aad string, i uint64, last bool) []byte {
	b := make([]byte, 0, len(aad)+9)
	b = append(b, aad...)
	b = binary.BigEndian.AppendUint64(b, i)
	if last {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	return b
}

type sealWriter struct {
	w    io.Writer
	gcm  cipher.AEAD
	aad  string
	buf  []byte
	n    uint64
	done bool
}

// SealStream returns a writer that encrypts with the space data key in 64 KiB segments
// bound to aad. Close writes the final segment.
func (v *Vault) SealStream(ctx context.Context, spaceID uuid.UUID, w io.Writer, aad string) (io.WriteCloser, error) {
	k, err := v.dataKey(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	g, err := gcm(k.key)
	if err != nil {
		return nil, err
	}
	hdr := append([]byte(streamMagic), byte(k.version))
	if _, err := w.Write(hdr); err != nil {
		return nil, err
	}
	return &sealWriter{w: w, gcm: g, aad: aad, buf: make([]byte, 0, SegmentSize)}, nil
}

func (s *sealWriter) flush(last bool) error {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	ct := s.gcm.Seal(nil, nonce, s.buf, segAAD(s.aad, s.n, last))
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(ct)))
	for _, b := range [][]byte{l[:], nonce, ct} {
		if _, err := s.w.Write(b); err != nil {
			return err
		}
	}
	s.n++
	s.buf = s.buf[:0]
	return nil
}

func (s *sealWriter) Write(p []byte) (int, error) {
	if s.done {
		return 0, errors.New("vault: write after close")
	}
	total := len(p)
	for len(p) > 0 {
		room := SegmentSize - len(s.buf)
		take := min(room, len(p))
		s.buf = append(s.buf, p[:take]...)
		p = p[take:]
		if len(s.buf) == SegmentSize && len(p) > 0 {
			if err := s.flush(false); err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}

func (s *sealWriter) Close() error {
	if s.done {
		return nil
	}
	s.done = true
	return s.flush(true)
}

type openReader struct {
	r    *bufio.Reader
	gcm  cipher.AEAD
	aad  string
	n    uint64
	buf  []byte
	last bool
}

// OpenStream decrypts the output of SealStream. A wrong key, a changed byte, a reordered,
// missing or extra segment makes Read fail.
func (v *Vault) OpenStream(ctx context.Context, spaceID uuid.UUID, r io.Reader, aad string) (io.Reader, error) {
	br := bufio.NewReaderSize(r, SegmentSize+64)
	hdr := make([]byte, len(streamMagic)+1)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return nil, fmt.Errorf("vault: archive header: %w", err)
	}
	if string(hdr[:len(streamMagic)]) != streamMagic {
		return nil, errors.New("vault: not an encrypted archive")
	}
	k, err := v.dataKey(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	if int(hdr[len(streamMagic)]) != k.version {
		return nil, fmt.Errorf("vault: archive key version %d, current %d", hdr[len(streamMagic)], k.version)
	}
	g, err := gcm(k.key)
	if err != nil {
		return nil, err
	}
	return &openReader{r: br, gcm: g, aad: aad}, nil
}

func (o *openReader) next() error {
	var l [4]byte
	if _, err := io.ReadFull(o.r, l[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return ErrDecrypt // the final segment is missing
		}
		return err
	}
	n := binary.BigEndian.Uint32(l[:])
	if n > SegmentSize+uint32(o.gcm.Overhead()) {
		return ErrDecrypt
	}
	nonce := make([]byte, o.gcm.NonceSize())
	if _, err := io.ReadFull(o.r, nonce); err != nil {
		return ErrDecrypt
	}
	ct := make([]byte, n)
	if _, err := io.ReadFull(o.r, ct); err != nil {
		return ErrDecrypt
	}
	// Try as a middle segment first, then as the last one.
	if p, err := o.gcm.Open(nil, nonce, ct, segAAD(o.aad, o.n, false)); err == nil {
		o.buf, o.n = p, o.n+1
		return nil
	}
	p, err := o.gcm.Open(nil, nonce, ct, segAAD(o.aad, o.n, true))
	if err != nil {
		return ErrDecrypt
	}
	o.buf, o.n, o.last = p, o.n+1, true
	if _, err := o.r.Peek(1); err == nil {
		return ErrDecrypt // data after the last segment
	}
	return nil
}

func (o *openReader) Read(p []byte) (int, error) {
	for len(o.buf) == 0 {
		if o.last {
			return 0, io.EOF
		}
		if err := o.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, o.buf)
	o.buf = o.buf[n:]
	return n, nil
}
