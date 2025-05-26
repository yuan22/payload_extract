package payload_extract_go

import (
	"archive/zip"
	"errors"
	"io"
	"strings"
	"sync"
)

type ZipPayloadReader struct {
	//zr *zip.Reader // zip reader
	zf *zip.File
	or io.ReaderAt // origin reader

	dataoff int64 // store method use

	pos int64

	stream       io.ReadCloser
	streamStart  int64
	streamOffset int64

	mu sync.Mutex
}

func (r *ZipPayloadReader) ReadAt(p []byte, off int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.zf.Method == zip.Store { // If zip compress method is Store, jump to offset return data
		writelen, err := r.or.ReadAt(p, r.dataoff+off)
		if err != nil {
			return 0, err
		}
		r.pos += int64(writelen)
		return writelen, err
	} else {
		if r.stream == nil || r.streamStart+r.streamOffset != off { // Reuse stream to avoid read save speed
			if r.stream != nil {
				r.stream.Close()
				r.stream = nil
			}
			var err error
			r.stream, err = r.zf.Open()
			if err != nil {
				return 0, err
			}

			io.CopyN(io.Discard, r.stream, off)
			r.streamStart = off
			r.streamOffset = 0
		}

		writelen, err := r.stream.Read(p)
		if err != nil {
			return 0, err
		}
		r.streamOffset += int64(writelen)
		r.pos += int64(writelen)

		return writelen, err
	}
}

func (r *ZipPayloadReader) Read(p []byte) (int, error) {
	if r.zf.Method == zip.Store { // If zip compress method is Store, jump to offset return data
		writelen, err := r.or.ReadAt(p, r.dataoff+r.pos)
		if err != nil {
			return 0, err
		}
		r.pos += int64(writelen)
		return writelen, err
	} else {
		if r.stream == nil || r.streamStart+r.streamOffset != r.pos { // Reuse stream to avoid read save speed
			if r.stream != nil {
				r.stream.Close()
				r.stream = nil
			}
			var err error
			r.stream, err = r.zf.Open()
			if err != nil {
				return 0, err
			}

			io.CopyN(io.Discard, r.stream, r.pos)
			r.streamStart = r.pos
			r.streamOffset = 0
		}

		writelen, err := r.stream.Read(p)
		if err != nil {
			return 0, err
		}
		r.streamOffset += int64(writelen)
		r.pos += int64(writelen)

		return writelen, err
	}
}

func (r *ZipPayloadReader) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		r.pos = off
	case io.SeekCurrent:
		r.pos += off
	case io.SeekEnd:
		r.pos = int64(r.zf.UncompressedSize64) + off
	default:
		return 0, errors.New("unsupported whence")
	}

	r.pos = min(int64(r.zf.UncompressedSize64-1), r.pos)

	return r.pos, nil
}

func (r *ZipPayloadReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stream != nil {
		return r.stream.Close()
	}
	return nil // stub
}

func NewZipPayloadReader(reader io.ReaderAt, size int64) (*ZipPayloadReader, error) {
	zr, err := zip.NewReader(reader, size)
	if err != nil {
		return nil, err
	}

	var zf *zip.File = nil
	for _, file := range zr.File {
		if strings.HasSuffix(file.Name, "payload.bin") {
			zf = file
			break // save time
		}
	}
	if zf == nil {
		return nil, errors.New("could not found payload.bin in zip file")
	}

	dataoff, err := zf.DataOffset()
	if err != nil {
		return nil, errors.New("could not found payload.bin data offset")
	}

	Logger.Println("Zip compress method:", func() string {
		if zf.Method == zip.Store {
			return "Store"
		}
		return "Deflate"
	}())

	return &ZipPayloadReader{
		zf:           zf,
		or:           reader,
		dataoff:      dataoff,
		pos:          0,
		streamStart:  0,
		streamOffset: 0,
	}, nil
}
