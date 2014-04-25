package main

import (
	"testing"
	"io/ioutil"
	"syscall"
	"os"
	"net/http"
	"fmt"
	"encoding/json"
)

func expectUrlPathsInFile(t *testing.T, contents string, expected []string) ([]*http.Request) {
	f, err := ioutil.TempFile("", "testcontent")
	if err != nil { panic(err) }
	defer syscall.Unlink(f.Name())

	ioutil.WriteFile(f.Name(), []byte(contents), (os.FileMode)(0644))

	r := NewInfiniteRequestReader(f.Name())
	defer r.Close()

	requests := make([]*http.Request, len(expected))

	for i, e := range expected {
		request := r.NextRequest()

		if request.URL.Path != e {
			t.Fatalf("Expected [%s] but got [%s]", e, request)
		}

		requests[i] = request
	}

	return requests
}

func TestTwoRequestsNoHeaders(t *testing.T) {
	expectUrlPathsInFile(t,
		"GET /foo HTTP/1.1\n\nGET /bar HTTP/1.1\n\n",
		[]string { "/foo", "/bar" })
}

func TestInfiniteRequestReading(t *testing.T) {
	expectUrlPathsInFile(t,
		"GET /foo HTTP/1.1\n\nGET /bar HTTP/1.1\n\n",
		[]string { "/foo", "/bar", "/foo", "/bar" })
}

func TestTwoRequestsWithHeaders(t *testing.T) {
	requests := expectUrlPathsInFile(t,
		"GET /foo HTTP/1.1\nHost: localhost\nX-Test: Test1\n\nGET /bar HTTP/1.1\nHost: localhost\nX-Test: Test2\n\n",
		[]string { "/foo", "/bar" })

	for _, req := range requests {
		if req.Host != "localhost" {
			t.Errorf("Expected [%s] but got [%s]", "localhost", req.Host)
		}
	}

	for i, req := range requests {
		expectedValue := fmt.Sprintf("Test%d", i+1)
		if testHeader := req.Header.Get("X-Test"); testHeader != expectedValue {
			t.Errorf("Expected [%s] but got [%s]", expectedValue, testHeader)
		}
	}
}

func TestTwoRequestsWithHeadersAndBody(t *testing.T) {
	requests := expectUrlPathsInFile(t,
		"POST /foo HTTP/1.1\nHost: localhost\nContent-Type: application/json\nContent-Length: 18\n\n{\n\n\"test\":true\n\n}\n" +
			"POST /bar HTTP/1.1\nHost: localhost\nContent-Type: application/json\nContent-Length: 18\n\n{\n\n\"test\":true\n\n}\n",
		[]string { "/foo", "/bar" })

	for _, req := range requests {
		if req.Host != "localhost" {
			t.Errorf("Expected [%s] but got [%s]", "localhost", req.Host)
		}
	}

	for _, req := range requests {
		var data map[string]bool
		err := json.NewDecoder(req.Body).Decode(&data)

		if err != nil {
			t.Errorf("unable to decode json: %v", err)
		} else if !data["test"] {
			t.Errorf("unexpected json data: %v", data)
		}

	}
}
