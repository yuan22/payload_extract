package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	payload_extract "github.com/affggh/payload_extract"
)

type action int

const (
	ACTION_SHOW_PARTITION_INFO action = iota
	ACTION_EXTRACT_PARTITION
)

type payload_type int

const (
	TYPE_BIN payload_type = iota
	TYPE_ZIP
	TYPE_URL
)

const Version = "Unknow-dirty"

type config struct {
	input       string
	outdir      string
	partitions  []string
	workers     int
	act         action
	_type       payload_type
	showVersion bool
}

func main() {
	cfg := config{
		outdir:      "out",
		partitions:  []string{},
		workers:     12,
		act:         ACTION_EXTRACT_PARTITION,
		_type:       TYPE_BIN,
		showVersion: false,
	}

	flag.StringVar(&cfg.input, "i", "", "input payload bin/zip/url")
	flag.StringVar(&cfg.outdir, "o", "out", "output directory")
	flag.Func("X", "extract partitions", func(s string) error {
		cfg.partitions = strings.Split(s, ",")
		return nil
	})
	flag.IntVar(&cfg.workers, "T", 12, "thread pool workers")
	flag.BoolFunc("P", "do not extract, print partitions info", func(s string) error {
		cfg.act = ACTION_SHOW_PARTITION_INFO
		return nil
	})
	flag.BoolVar(&cfg.showVersion, "v", false, "print version and exit")

	flag.Parse()

	if cfg.showVersion {
		fmt.Println("- Version:", Version)
		os.Exit(0)
	}

	if len(cfg.input) == 0 {
		log.Fatalln("Must spec input file!")
	}

	// Detect input type
	if strings.HasPrefix(cfg.input, "http://") || strings.HasPrefix(cfg.input, "https://") {
		cfg._type = TYPE_URL
	} else {
		fd, err := os.Open(cfg.input)
		if err != nil {
			log.Fatalln(err)
		}
		buf := make([]byte, 4)
		_, err = fd.Read(buf)
		if err != nil {
			log.Fatalln(err)
		}

		if bytes.Equal(buf, []byte("PK\x03\x04")) {
			cfg._type = TYPE_ZIP
		} else {
			cfg._type = TYPE_BIN // raw payload.bin
		}

		fd.Close()
	}

	// Adjust reader
	var reader io.ReadSeekCloser
	var err error

	switch cfg._type {
	case TYPE_URL:
		urlreder := payload_extract.NewUrlRangeReaderAt(cfg.input)
		defer urlreder.Close()

		reader, err = payload_extract.NewZipPayloadReader(urlreder, urlreder.Size())
		if err != nil {
			log.Fatalln(err)
		}
	case TYPE_ZIP:
		fd, err := os.Open(cfg.input)
		if err != nil {
			log.Fatalln(err)
		}
		defer fd.Close()

		size, _ := fd.Seek(0, io.SeekEnd)
		reader, err = payload_extract.NewZipPayloadReader(fd, size)
		if err != nil {
			log.Fatalln(err)
		}
	case TYPE_BIN:
		reader, err = os.Open(cfg.input)
		if err != nil {
			log.Fatalln(err)
		}
	default:
		log.Fatalln("Unsupported input type")

	}
	defer reader.Close()

	// Do payload action
	switch cfg.act {
	case ACTION_EXTRACT_PARTITION:
		payload_extract.ExtractPartitionsFromPayload(reader, cfg.partitions, cfg.outdir, cfg.workers)
	case ACTION_SHOW_PARTITION_INFO:
		manifest, err := payload_extract.InitPayloadInfo(reader)
		if err != nil {
			log.Fatalln(err)
		}
		payload_extract.PrintPartitionsInfo(manifest, cfg.partitions)
	default:
		log.Fatalln("Unsupport action")
	}
}
