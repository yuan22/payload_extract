package payload_extract

import (
	"archive/zip"
	"errors"
	"io"
	"log"
	"strings"
)

type ZipFileSeekReader struct {
	freader io.ReaderAt
	zreader *zip.Reader
	zfile   *zip.File
	pos     int64
	total   int64

	lastpos int64

	zfilereader io.ReadCloser
}

func (r *ZipFileSeekReader) Close() error {
	if r.zfilereader != nil {
		return r.zfilereader.Close()
	}
	return nil
}

func (r *ZipFileSeekReader) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekStart {
		r.pos = offset
	} else if whence == io.SeekCurrent {
		r.pos += offset
	} else if whence == io.SeekEnd {
		r.pos = r.total
	} else {
		return -1, errors.New("whence not support")
	}

	r.pos = min(r.pos, r.total-1)

	return r.pos, nil
}

func (r *ZipFileSeekReader) Read(data []byte) (int, error) {
	if r.zfilereader == nil {
		var err error = nil
		r.zfilereader, err = r.zfile.Open()
		if err != nil {
			return -1, err
		}
	}

	if r.pos < 0 {
		return -1, errors.New("pos is less than 0")
	}

	if r.pos < r.lastpos {
		log.Println("Read position is less than last position, re-open zip file")
		r.zfilereader.Close()
		r.zfilereader, _ = r.zfile.Open() // re-open
		io.CopyN(io.Discard, r.zfilereader, r.pos)
	}

	if r.pos > r.lastpos {
		log.Println("Read position is large than last position, skip", r.pos-r.lastpos, "bytes")
		io.CopyN(io.Discard, r.zfilereader, r.pos-r.lastpos) // skip
	}

	write_len, _ := r.zfilereader.Read(data)
	r.pos += int64(write_len)
	r.lastpos = r.pos

	return write_len, nil
}

func NewZipFileSeekReader(reader io.ReaderAt, size int64) (*ZipFileSeekReader, error) {
	zreader, err := zip.NewReader(reader, size)
	if err != nil {
		log.Fatalln(err)
	}

	var zfile *zip.File = nil
	for _, zf := range zreader.File {
		if strings.HasSuffix(zf.Name, "payload.bin") {
			zfile = zf
			break
		}
	}

	if zfile == nil {
		return nil, errors.New("could not found payload.bin in zip archive")
	}

	return &ZipFileSeekReader{
		freader: reader,
		zreader: zreader,
		zfile:   zfile,
		pos:     int64(0),
		total:   size,
		lastpos: int64(0),
	}, nil
}
