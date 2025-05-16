package payload_extract

import (
	"bytes"
	"compress/bzip2"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strconv"

	"github.com/affggh/payload_extract/update_metadata"
	"github.com/ulikunitz/xz"
	"google.golang.org/protobuf/proto"
)

func badPayload(msg any) error {
	switch msg := msg.(type) {
	case string:
		return errors.New("invalid payload: " + msg)
	default:
		return fmt.Errorf("invalid payload: %v", msg)
	}
}

const PAYLOAD_MAGIC = "CrAU"

type PayloadCommonHdr struct {
	Magic          [4]byte
	Version        uint64
	ManifestLen    uint64
	ManifestSigLen uint32
}

func doExtractBootFromPayload(
	in_path string,
	partition_name *string,
	out_path *string,
) error {
	reader := func() *os.File {
		if in_path == "-" {
			return os.Stdin
		} else {
			fd, err := os.Open(in_path)
			if err != nil {
				log.Fatalln(err)
			}
			return fd
		}
	}()

	hdr := PayloadCommonHdr{}

	err := binary.Read(reader, binary.BigEndian, &hdr)
	if err != nil {
		return err
	}

	if !bytes.Equal(hdr.Magic[:], []byte(PAYLOAD_MAGIC)) {
		return badPayload("invalid magic")
	}

	if hdr.Version != 2 {
		return badPayload("unsupported version: " + strconv.FormatUint(hdr.Version, 10))
	}

	if hdr.ManifestLen == 0 {
		return badPayload("manifest length is zero")
	}

	if hdr.ManifestSigLen == 0 {
		return badPayload("manifest signature length is zero")
	}

	buf := make([]byte, hdr.ManifestLen)
	reader.Read(buf)
	manifest := new(update_metadata.DeltaArchiveManifest)
	if err := proto.Unmarshal(buf, manifest); err != nil {
		return err
	}
	if manifest.GetMinorVersion() != 0 {
		return badPayload("delta payloads are not supported, please use a full payload file")
	}

	block_size := manifest.GetBlockSize()

	partition := func() *update_metadata.PartitionUpdate {
		switch partition_name {
		case nil:
			var boot *update_metadata.PartitionUpdate = nil
			for _, p := range manifest.GetPartitions() {
				if p.GetPartitionName() == "init_boot" {
					boot = p
					break
				}
			}
			boot = func() *update_metadata.PartitionUpdate {
				if boot == nil {
					for _, p := range manifest.GetPartitions() {
						if p.GetPartitionName() == "boot" {
							return p
						}
					}
					return nil
				} else {
					return boot
				}
			}()
			if boot != nil {
				log.Fatalln(badPayload("boot partition not found"))
			}
		default:
			for _, p := range manifest.GetPartitions() {
				if p.GetPartitionName() == *partition_name {
					return p
				}
			}
			log.Fatalln(badPayload("partition " + *partition_name + " not found"))
		}
		return nil
	}()

	var out_str string
	out_path = func() *string {
		switch out_path {
		case nil:
			out_str = fmt.Sprintf("%s.img", partition.GetPartitionName())
			return &out_str
		default:
			return out_path
		}
	}()

	out_file, err := os.Create(*out_path)
	if err != nil {
		return err
	}
	defer out_file.Close()

	// Skip the manifest signature
	io.CopyN(io.Discard, reader, int64(hdr.ManifestSigLen))

	operations := partition.GetOperations()
	sort.Slice(operations, func(i, j int) bool {
		return operations[i].GetDataOffset() < operations[j].GetDataOffset()
	})

	var curr_data_offset uint64 = 0

	for _, operation := range operations {
		data_len := operation.GetDataLength()
		data_offset := operation.GetDataOffset()

		data_type := operation.GetType()

		buf = make([]byte, data_len)

		// Skip to the next offset and read data
		skip := data_offset - curr_data_offset
		io.CopyN(io.Discard, reader, int64(skip))
		reader.Read(buf)
		curr_data_offset = data_offset + data_len

		out_offset := operation.GetDstExtents()[0].GetStartBlock() * uint64(block_size)

		switch data_type {
		case update_metadata.InstallOperation_REPLACE:
			out_file.Seek(int64(out_offset), io.SeekStart)
			out_file.Write(buf)
		case update_metadata.InstallOperation_ZERO:
			for _, ext := range operation.GetDstExtents() {
				out_seek := ext.GetStartBlock() * uint64(block_size)
				num_blocks := ext.GetNumBlocks()
				out_file.Seek(int64(out_seek), io.SeekStart)
				out_file.Write(make([]byte, num_blocks))
			}
		case update_metadata.InstallOperation_REPLACE_BZ:
			reader := bzip2.NewReader(bytes.NewReader(buf))
			out_file.Seek(int64(out_offset), io.SeekStart)
			io.Copy(out_file, reader)
		case update_metadata.InstallOperation_REPLACE_XZ:
			reader, err := xz.NewReader(bytes.NewReader(buf))
			if err != nil {
				log.Fatalln(err)
			}
			out_file.Seek(int64(out_offset), io.SeekStart)
			io.Copy(out_file, reader)

		default:
			return badPayload("unsupported operation type")
		}
	}
	return nil
}

func ExtractBootFromPayload(in_path, partition, out_path string) bool {
	var p, o *string = nil, nil

	if len(partition) != 0 {
		p = &partition
	}
	if len(out_path) != 0 {
		o = &out_path
	}

	err := doExtractBootFromPayload(in_path, p, o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to extract from payload")
		fmt.Fprintln(os.Stderr, err)
		return false
	}
	return true
}
