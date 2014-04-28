package main

import (
	"bufio"
	"compress/gzip"
	"log"
	"os"
	"strings"
)

type InfiniteLineReader struct {
	path        string
	file        *os.File
	scanner     *bufio.Scanner
	lineCounter int
}

func (ilr *InfiniteLineReader) init() {
	if ilr.file == nil {
		file, err := os.Open(ilr.path)

		if err != nil {
			log.Fatalf("Failed: %v", err)
		}

		ilr.file = file
	} else {
		ilr.file.Seek(0, 0)
	}

	if strings.HasSuffix(ilr.path, ".gz") {
		zhandle, err := gzip.NewReader(ilr.file)

		if err != nil {
			log.Fatalf("Failed to create gzip reader for file %s: %v", ilr.path, err)
		}

		ilr.scanner = bufio.NewScanner(zhandle)
	} else {
		ilr.scanner = bufio.NewScanner(ilr.file)
	}

	ilr.lineCounter = 0
}

func NewInfiniteLineReader(path string) (result *InfiniteLineReader) {
	result = &InfiniteLineReader{
		path:        path,
		file:        nil,
		scanner:     nil,
		lineCounter: 0,
	}
	result.init()
	return
}

func (ilr *InfiniteLineReader) NextLine() string {
	if ilr.scanner.Scan() {
		ilr.lineCounter += 1
		return ilr.scanner.Text()

	} else {
		if ilr.scanner.Err() != nil {
			log.Fatalf("Scanner error: %v", ilr.scanner.Err())
		}

		if ilr.lineCounter == 0 {
			log.Fatal("Empty file detected: " + ilr.path)
		}

		ilr.init()

		return ilr.NextLine()
	}
}

func (ilr *InfiniteLineReader) Close() {
	if ilr.file != nil {
		ilr.file.Close()
	}

	ilr.file = nil
	ilr.scanner = nil
	ilr.lineCounter = 0
}
