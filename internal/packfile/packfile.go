package packfile

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
)

// ObjectType represents a Git object type in a packfile.
type ObjectType byte

const (
	ObjectBlob     ObjectType = 3
	ObjectTree     ObjectType = 2
	ObjectOFSDelta ObjectType = 6
	ObjectRefDelta ObjectType = 7
)

// Object represents a single object in a packfile.
type Object struct {
	Type ObjectType
	Data []byte
	SHA  [20]byte
}

// FileRef tracks a file added to the packfile for listing.
type FileRef struct {
	Name string
	SHA  string
	Size int
}

// Writer generates Git-format packfiles.
type Writer struct {
	objects []Object
	files   []FileRef
}

// NewWriter creates a new packfile writer.
func NewWriter() *Writer {
	return &Writer{}
}

// AddBlob adds raw file content as a blob object.
func (w *Writer) AddBlob(data []byte) [20]byte {
	header := fmt.Sprintf("blob %d\x00", len(data))
	sha := sha1.Sum(append([]byte(header), data...))

	w.objects = append(w.objects, Object{
		Type: ObjectBlob,
		Data: data,
		SHA:  sha,
	})
	return sha
}

// AddTree adds a tree object mapping names to object SHAs.
func (w *Writer) AddTree(entries []TreeEntry) [20]byte {
	var buf bytes.Buffer
	for _, e := range entries {
		fmt.Fprintf(&buf, "%s %s\x00", e.Mode, e.Name)
		buf.Write(e.SHA[:])
	}
	data := buf.Bytes()

	header := fmt.Sprintf("tree %d\x00", len(data))
	sha := sha1.Sum(append([]byte(header), data...))

	w.objects = append(w.objects, Object{
		Type: ObjectTree,
		Data: data,
		SHA:  sha,
	})
	return sha
}

// WriteTo writes the complete packfile to w.
func (pw *Writer) WriteTo(w io.Writer) (int64, error) {
	var total int64

	// Header: PACK + version(2) + num objects
	header := make([]byte, 12)
	copy(header[0:4], "PACK")
	binary.BigEndian.PutUint32(header[4:8], 2) // version
	binary.BigEndian.PutUint32(header[8:12], uint32(len(pw.objects)))

	n, err := w.Write(header)
	total += int64(n)
	if err != nil {
		return total, err
	}

	// Write each object
	h := sha1.New()
	h.Write(header)

	for _, obj := range pw.objects {
		objBytes, err := encodeObject(obj)
		if err != nil {
			return total, fmt.Errorf("encoding object: %w", err)
		}
		n, err := w.Write(objBytes)
		total += int64(n)
		h.Write(objBytes)
		if err != nil {
			return total, err
		}
	}

	// Trailing checksum
	checksum := h.Sum(nil)
	n, err = w.Write(checksum)
	total += int64(n)
	return total, err
}

func encodeObject(obj Object) ([]byte, error) {
	var buf bytes.Buffer

	// Object header: type + size in variable-length encoding
	size := len(obj.Data)
	typeBits := byte(obj.Type) << 4

	// First byte: 1-bit continuation + 3-bit type + 4-bit size
	first := typeBits | byte(size&0x0f)
	size >>= 4
	if size > 0 {
		first |= 0x80
	}
	buf.WriteByte(first)

	// Remaining size bytes
	for size > 0 {
		b := byte(size & 0x7f)
		size >>= 7
		if size > 0 {
			b |= 0x80
		}
		buf.WriteByte(b)
	}

	// Zlib-compressed data
	zlibBuf := &bytes.Buffer{}
	zw := zlib.NewWriter(zlibBuf)
	if _, err := zw.Write(obj.Data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	buf.Write(zlibBuf.Bytes())

	return buf.Bytes(), nil
}

// TreeEntry represents a single entry in a tree object.
type TreeEntry struct {
	Mode string   // "100644" for files, "040000" for directories
	Name string   // filename
	SHA  [20]byte // object SHA
}

// AddFile records a named file blob for manifest purposes.
func (w *Writer) AddFile(name string, data []byte) {
	header := fmt.Sprintf("blob %d\x00", len(data))
	sha := sha1.Sum(append([]byte(header), data...))
	w.files = append(w.files, FileRef{
		Name: name,
		SHA:  fmt.Sprintf("%x", sha),
		Size: len(data),
	})
}

// ObjectCount returns the number of objects in the packfile.
func (w *Writer) ObjectCount() int {
	return len(w.objects)
}

// Files returns the list of named files added.
func (w *Writer) Files() []FileRef {
	return w.files
}

// Reader parses a packfile stream.
type Reader struct {
	// TODO: Implement packfile reading for delta application
}

// Delta computes the delta between two object sets for incremental updates.
func Delta(have [][20]byte, current []Object) []Object {
	// TODO: Compute minimal delta set
	// Objects in `current` whose SHA is not in `have` need to be sent
	haveSet := make(map[[20]byte]bool, len(have))
	for _, sha := range have {
		haveSet[sha] = true
	}

	var delta []Object
	for _, obj := range current {
		if !haveSet[obj.SHA] {
			delta = append(delta, obj)
		}
	}
	return delta
}
