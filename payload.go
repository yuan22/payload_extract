package payload_extract_go

import (
	"bytes"
	"compress/bzip2"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"slices"
	"sort"
	"sync"

	"github.com/affggh/payload_extract/update_engine"
	"github.com/spencercw/go-xz"

	"github.com/panjf2000/ants/v2"
	"github.com/schollz/progressbar/v3"
)

var Logger = log.New(log.Writer(), "payload_extract:", log.Flags())

const PAYLOAD_MAGIC = "CrAU"

func BadPayload(msg any) error {
	switch v := msg.(type) {
	case string:
		return errors.New("invalid payload: " + v)
	case error:
		return fmt.Errorf("invalid payload: %w", v)
	case fmt.Stringer:
		return errors.New("invalid payload: " + v.String())
	default:
		return fmt.Errorf("invalid payload: %v", msg)
	}
}

type PayloadHdr struct {
	Magic          [4]byte
	Version        uint64
	ManifestLen    uint64
	ManifestSigLen uint32
}

func (p *PayloadHdr) Decode(data []byte) error {
	if len(data) < binary.Size(*p) {
		return BadPayload("invalid data size to decode to hdr")
	}

	_, err := binary.Decode(data, binary.BigEndian, p)
	return err
}

func (p *PayloadHdr) HdrSize() int {
	return binary.Size(*p)
}

func InitPayloadInfo(reader io.ReadSeeker) (*update_engine.DeltaArchiveManifest, error) {
	hdr := PayloadHdr{}

	binary.Read(reader, binary.BigEndian, &hdr)

	//fmt.Printf("%v\n", hdr)

	if !bytes.Equal(hdr.Magic[:], []byte(PAYLOAD_MAGIC)) {
		return nil, BadPayload("invalid magic")
	}
	if hdr.Version != 2 {
		Logger.Println("Warning: payload version is", hdr.Version, "which is not equal to 2!")
	}
	if hdr.ManifestLen == 0 {
		return nil, BadPayload("manifest length is zero")
	}
	if hdr.ManifestSigLen == 0 {
		return nil, BadPayload("manifest signature length is zero")
	}

	manifest := new(update_engine.DeltaArchiveManifest)
	buf := make([]byte, hdr.ManifestLen)
	_, err := reader.Read(buf)
	if err != nil {
		return nil, err
	}

	if err = manifest.Unmarshal(buf); err != nil {
		return nil, err
	}

	if manifest.GetMinorVersion() != 0 {
		return nil, BadPayload("delta payloads are not supported, please use a full payload file")
	}

	// Skip signature
	reader.Seek(int64(hdr.ManifestSigLen), io.SeekCurrent)
	//io.CopyN(io.Discard, reader, int64(hdr.ManifestSigLen))

	return manifest, nil
}

func PrintPartitionsInfo(manifest *update_engine.DeltaArchiveManifest, partitions_name []string) {
	fmt.Println("Payload Info:")
	fmt.Println("\tPatch Level:", manifest.SecurityPatchLevel)
	fmt.Println("\tBlock Size:", *manifest.BlockSize)
	fmt.Println("\tMinor Version:", *manifest.MinorVersion)
	fmt.Println("\tMax Time Stamp:", manifest.MaxTimestamp)
	fmt.Println("\tApex Info:", len(manifest.ApexInfo))
	fmt.Println("\t\t", "PackageName", "Version", "IsCompressed", "DecompressedSize")
	for _, i := range manifest.ApexInfo {
		fmt.Println("\t\t", i.PackageName, i.Version, i.IsCompressed, i.DecompressedSize)
	}
	fmt.Println("\tPartitions:", len(manifest.Partitions))
	fmt.Println("\t\t", "PartitionName", "PartitionSize")
	parts := make([]*update_engine.PartitionUpdate, 0)
	if len(partitions_name) == 0 {
		parts = manifest.Partitions
	} else {
		for _, p := range manifest.Partitions {
			if slices.ContainsFunc(partitions_name, func(part string) bool {
				return p.PartitionName == part
			}) {
				parts = append(parts, p)
			}
		}
	}
	for _, p := range parts {
		partition_size := func() int64 {
			last_operation, _ := last(p.Operations)
			last_extents, _ := last(last_operation.DstExtents)

			return int64((last_extents.StartBlock + last_extents.NumBlocks) * uint64(*manifest.BlockSize))
		}()

		fmt.Printf("\t\t %-14s%d\n", p.PartitionName, partition_size)
	}
}

// 1MB Zero buffer
var zero_buffer = make([]byte, 1<<20)

func write_zero(writer io.WriterAt, size int64, offset int64) (int64, error) {
	total_write := int64(0)
	for total_write < size {
		bytesToWrite := int64(len(zero_buffer))
		if total_write+bytesToWrite > size {
			bytesToWrite = size - total_write
		}

		n, err := writer.WriteAt(zero_buffer[:bytesToWrite], total_write+offset)
		if err != nil {
			return total_write, err
		}
		total_write += int64(n)

		if n == 0 && bytesToWrite > 0 {
			return total_write, io.ErrNoProgress
		}
	}
	return total_write, nil
}

func extractOperationToFile(
	operation *update_engine.InstallOperation,
	writer io.WriterAt,
	out_offset int64,
	block_size int,
	data []byte,
	progress_bar *progressbar.ProgressBar,
	wg *sync.WaitGroup,
) error {
	defer wg.Done()
	var write_len int
	var err error
	switch operation.Type {
	case update_engine.REPLACE:
		write_len, err = writer.WriteAt(data, out_offset)
		if err != nil {
			return err
		}
	case update_engine.ZERO:
		for _, ext := range operation.GetDstExtents() {
			out_seek := ext.StartBlock * uint64(block_size)
			num_blocks := ext.NumBlocks

			xlen, err := write_zero(writer, int64(num_blocks), int64(out_seek))
			if err != nil {
				return err
			}
			write_len += int(xlen)
		}
	case update_engine.REPLACE_BZ, update_engine.REPLACE_XZ:
		var zreader io.Reader
		var breader = bytes.NewReader(data)
		if operation.Type == update_engine.REPLACE_BZ {
			zreader = bzip2.NewReader(breader)
		} else if operation.Type == update_engine.REPLACE_XZ {
			xzreader := xz.NewDecompressionReader(breader)
			zreader = &xzreader
		}

		closer, ok := zreader.(io.Closer)
		if ok { // lzma need close
			defer closer.Close()
		}

		w := io.NewOffsetWriter(writer, out_offset)
		if l, err := io.Copy(w, zreader); err != nil {
			return err
		} else {
			write_len = int(l)
		}
	default:
		return BadPayload("unexpcted data type")
	}

	progress_bar.Add(write_len)
	return nil
}

func extractPartitionFromPayload(
	reader io.ReadSeeker,
	block_size int,
	partition *update_engine.PartitionUpdate,
	out_path string,
	total_size int,
	bar *progressbar.ProgressBar,
	pool *ants.Pool,
) error {
	fd, err := os.Create(out_path)
	if err != nil {
		return err
	}
	defer fd.Close()

	err = fd.Truncate(int64(total_size))
	if err != nil {
		defer os.Remove(out_path)
		return err
	}

	curr_data_offset := int64(0)

	operations := partition.Operations
	sort.Slice(operations, func(i, j int) bool {
		return operations[i].DataOffset < operations[j].DataOffset
	})

	var wg sync.WaitGroup
	//p, _ := ants.NewPool(runtime.NumCPU())
	//Logger.Println("Process", partition.GetPartitionName(), "with threads:", runtime.NumCPU())
	//defer p.Release()

	for _, operation := range operations {
		data_len := operation.DataLength
		data_offset := operation.DataOffset

		reader.Seek(int64(data_offset)-curr_data_offset, io.SeekCurrent)

		//data, err := io.ReadAll(io.LimitReader(reader, int64(data_len)))
		//if err != nil {
		//	return err
		//}
		data := make([]byte, data_len)
		_, err = reader.Read(data)
		if err != nil {
			return err
		}

		curr_data_offset = int64(data_offset + data_len)
		wg.Add(1)
		err = pool.Submit(func() {
			err := extractOperationToFile(
				operation,
				fd,
				int64(operation.GetDstExtents()[0].GetStartBlock()*uint64(block_size)),
				block_size,
				data,
				bar,
				&wg,
			)
			if err != nil {
				Logger.Printf("Error: %v", err)
			}
		})
		if err != nil {
			return err
		}
	}
	wg.Wait()

	return nil
}

// go1.18+
func last[T any](s []T) (T, bool) {
	if len(s) == 0 {
		var zero T
		return zero, false
	}
	return s[len(s)-1], true
}

func ExtractPartitionsFromPayload(
	reader io.ReadSeeker,
	partitions_name []string,
	out_dir string,
	max_workers int,
) {
	reader.Seek(0, io.SeekStart)

	os.RemoveAll(out_dir)
	os.MkdirAll(out_dir, 0777)

	manifest, err := InitPayloadInfo(reader)
	if err != nil {
		log.Fatalln(err)
	}

	baseoff, _ := reader.Seek(0, io.SeekCurrent)

	var all_parts []*update_engine.PartitionUpdate
	if len(partitions_name) == 0 { // Extract all
		all_parts = manifest.Partitions
	} else {
		for _, p := range manifest.Partitions {
			if slices.Contains(partitions_name, p.PartitionName) {
				all_parts = append(all_parts, p)
			}
		}
	}

	block_size := *manifest.BlockSize

	pool, _ := ants.NewPool(max_workers)
	defer pool.Release()

	fmt.Println("Processing with threads:", max_workers)

	for idx, p := range all_parts {
		reader.Seek(baseoff, io.SeekStart)

		total_length := func() int64 {
			last_operation, _ := last(p.Operations)
			last_extents, _ := last(last_operation.DstExtents)

			return int64((last_extents.StartBlock + last_extents.NumBlocks) * uint64(block_size))
		}()

		bar := progressbar.NewOptions64(total_length,
			progressbar.OptionSetWriter(os.Stderr), //you should install "github.com/k0kubun/go-ansi"
			progressbar.OptionEnableColorCodes(true),
			progressbar.OptionShowBytes(true),
			progressbar.OptionShowTotalBytes(true),
			progressbar.OptionClearOnFinish(),
			progressbar.OptionSetWidth(15),
			progressbar.OptionSetDescription(fmt.Sprintf("[cyan][%d/%d][reset] Partition %-12s size: %-10d ...", idx+1, len(all_parts), p.GetPartitionName(), total_length)),
			progressbar.OptionSetTheme(progressbar.Theme{
				Saucer:        "[green]#[reset]",
				SaucerHead:    "[green]>[reset]",
				SaucerPadding: "_",
				BarStart:      "[",
				BarEnd:        "]",
			}))

		fmt.Println("Extracting", p.PartitionName, "...")
		err := extractPartitionFromPayload(reader, int(block_size), p, path.Join(out_dir, p.PartitionName+".img"), int(total_length), bar, pool)
		if err != nil {
			log.Println(err)
		}

		bar.Finish()
	}

	fmt.Println("Done!")
}
