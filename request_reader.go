package main

import (
	"bufio"
	"compress/gzip"
	"log"
	"os"
	"strings"
	"net/http"
	"io"
	"bytes"
	"io/ioutil"
)

type InfiniteRequestReader struct {
	path           string
	file           *os.File
	reader         *bufio.Reader
	requestCounter int
}

func (irr *InfiniteRequestReader) init() {
	if irr.file == nil {
		file, err := os.Open(irr.path)

		if err != nil {
			log.Fatalf("Failed: %v", err)
		}

		irr.file = file
	} else {
		irr.file.Seek(0, 0)
	}

	if strings.HasSuffix(irr.path, ".gz") {
		zhandle, err := gzip.NewReader(irr.file)

		if err != nil {
			log.Fatalf("Failed to create gzip reader for file %s: %v", irr.path, err)
		}

		irr.reader = bufio.NewReader(zhandle)
	} else {
		irr.reader = bufio.NewReader(irr.file)
	}

	irr.requestCounter = 0
}

func NewInfiniteRequestReader(path string) (result *InfiniteRequestReader) {
	result = &InfiniteRequestReader{
		path:    		path,
		file:    		nil,
		reader:  		nil,
		requestCounter: 0,
	}
	result.init()
	return
}

func (irr *InfiniteRequestReader) NextRequest() *http.Request {
	if _, err := irr.reader.Peek(1); err == nil {
		req, err := http.ReadRequest(irr.reader)

		if err != nil {
			log.Fatalf("Request reader error: %v", err)
		}

		// Need to make request read any body buffer before continuing
		body := new(bytes.Buffer)
		body.ReadFrom(req.Body)
		req.Body = ioutil.NopCloser(body)

		irr.requestCounter += 1

		return req
	} else {
		if err != io.EOF {
			log.Fatalf("Request reader error: %v", err)
		}

		if irr.requestCounter == 0 {
			log.Fatal("File contains 0 requests: " + irr.path)
		}

		irr.init()
		return irr.NextRequest()
	}
}

func (irr *InfiniteRequestReader) Close() {
	if irr.file != nil {
		irr.file.Close()
	}

	irr.file = nil
	irr.reader = nil
	irr.requestCounter = 0
}
