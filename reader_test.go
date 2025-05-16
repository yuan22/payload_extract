package payload_extract_test

import (
	"encoding/binary"
	"io"
	"os"
	"testing"

	"github.com/affggh/payload_extract"
)

func TestZipReadSeeker(t *testing.T) {
	t.Log("Testing zip file seek")

	fd, err := os.Open("lineage-22.1-20250408-nightly-fajita-signed.zip")
	if err != nil {
		t.Fatal(err)
	}
	size, _ := fd.Seek(0, io.SeekEnd)
	zreader, err := payload_extract.NewZipFileSeekReader(fd, size)
	if err != nil {
		t.Fatal(err)
	}
	defer zreader.Close()

	// Test init read
	hdr := payload_extract.PayloadCommonHdr{}
	binary.Read(zreader, binary.BigEndian, &hdr)

	t.Logf("Test hdr: %v", hdr)

	// Test read at start
	zreader.Seek(0, io.SeekStart)
	buf := make([]byte, 4)
	zreader.Read(buf)

	t.Logf("Test: magic: %s", buf)

	// Test read continue
	var version uint64
	binary.Read(zreader, binary.BigEndian, &version)
	t.Logf("Test version: %d", version)

	// Test skip 8 bytes then read
	var manifest_sig_len uint32
	zreader.Seek(8, io.SeekCurrent)
	binary.Read(zreader, binary.BigEndian, &manifest_sig_len)
	t.Logf("Test manifest_sig_len: %d", manifest_sig_len)
}
