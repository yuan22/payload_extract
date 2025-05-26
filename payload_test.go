package payload_extract_go_test

import (
	"log"
	"os"
	"runtime"
	"testing"

	"net/http"
	_ "net/http/pprof"

	payload_extract "github.com/affggh/payload_extract"
)

func TestPayloadZip(t *testing.T) {
	//fd, err := os.Open("haotian-ota_full-OS2.0.117.0.VOBCNXM-user-15.0-bc4dfc3598.zip")
	//if err != nil {
	//	t.Fatal(err)
	//}
	//defer fd.Close()

	//size, _ := fd.Seek(0, io.SeekEnd)

	//reader, err := payload_extract.NewZipPayloadReader(fd, size)
	//if err != nil {
	//	t.Fatal(err)
	//}

	//manifest, err := payload_extract.InitPayloadInfo(reader)
	//if err != nil {
	//	t.Fatal(err)
	//}

	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()

	fd, err := os.Open("payload.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()

	payload_extract.ExtractPartitionsFromPayload(fd, []string{"system"}, "out2", runtime.NumCPU())
}

func TestPayloadInfo(t *testing.T) {
	t.Log("Testing payload info")

	fd, err := os.Open("payload.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()

	manifest, err := payload_extract.InitPayloadInfo(fd)
	if err != nil {
		t.Fatal(err)
	}

	payload_extract.PrintPartitionsInfo(manifest, []string{})
}
