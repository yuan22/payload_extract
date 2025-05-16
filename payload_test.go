package payload_extract_test

import (
	"testing"

	"github.com/affggh/payload_extract"
)

func TestPayload(t *testing.T) {
	t.Log("Try extract LOGO")

	if !payload_extract.ExtractBootFromPayload("../payload.bin", "vendor", "") {
		t.Fail()
	}

}
