package payload_extract_go_test

import (
	"io"
	"os"
	"testing"

	payload_extract_go "github.com/affggh/payload_extract"
)

func TestOPPO(t *testing.T) {
	t.Log("Testing oppo sucking url")

	url := "https://gauss-componentotacostmanual-cn.allawnfs.com/remove-60b04f6bb72afd6a787e5124474068dc/component-ota/25/04/14/4f487121b8f242f3a2170b92ff22b5a9.zip"

	reader := payload_extract_go.NewUrlRangeReaderAt(url)
	buf := make([]byte, 4)
	reader.ReadAt(buf, 0)

	//bar := progressbar.New64(reader.Size())

	t.Log(reader.Size())

	_reader := io.NewSectionReader(reader, 0, reader.Size())
	fd, err := os.Create("oppo-test.zip")
	if err != nil {
		t.Error(err)
	}
	defer fd.Close()

	//_writer := io.MultiWriter(fd, bar)

	io.Copy(fd, _reader)
}
