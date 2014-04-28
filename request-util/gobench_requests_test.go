package main

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func tryWriteRequest(url string) (*http.Request, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	out := new(bytes.Buffer)
	writeRequest(req, out)

	return http.ReadRequest(bufio.NewReader(out))

}

func TestRewriteVersionToV3(t *testing.T) {
	testUrl := "http://localhost/cms/mobile/v2/json/android/latestfeed.json"
	req, err := tryWriteRequest(testUrl)

	if err != nil {
		t.Fatalf("Unable to write request for url %s: %v", testUrl, err)
	}

	expectedURL, _ := url.Parse(testUrl)
	expectedPath := strings.Replace(expectedURL.Path, "v2", "v3", 1)
	if expectedPath != req.URL.Path {
		t.Fatalf("Expected %s but got %s", expectedPath, req.URL.Path)
	}
}

func TestAddNYTAppHeaderTo80PercentOfAndroidRequests(t *testing.T) {
	urls := []string{
		"http://localhost/cms/mobile/v3/json/android/homepage.json",
		"http://localhost/cms/mobile/v3/json/android_tablet/latestfeed.json",
		"http://localhost/cms/mobile/v3/json/kindle_fire/arts.json",
		"http://localhost/cms/mobile/v3/json/android/blogs/times_insider.json",
		"http://localhost/cms/mobile/v3/json/android_tablet/music.json",
		"http://localhost/cms/mobile/v3/json/iphone/latestfeed.json",
	}

	reqs := make([]*http.Request, len(urls))
	for i := 0; i < len(urls); i++ {
		req, err := tryWriteRequest(urls[i])

		if err != nil {
			t.Fatalf("Unable to write request for %s: %v", urls[i], err)
		} else {
			reqs[i] = req
		}
	}

	reqsWithVersion := make([]*http.Request, 0)
	reqsWithoutVersion := make([]*http.Request, 0)

	for _, req := range reqs {
		processRequest(req)

		version, hasVersion := req.Header[NytAppVersionHeader]
		if !hasVersion {
			reqsWithoutVersion = append(reqsWithoutVersion, req)
		} else if version[0] == AndroidInternationalVersion {
			reqsWithVersion = append(reqsWithVersion, req)
		}
	}

	if len(reqsWithoutVersion) != 2 {
		t.Errorf("Expected only 1 request without a version, but got %v", reqsWithoutVersion)
	}

	if len(reqsWithVersion) != 4 {
		t.Errorf("Expected only 4 requests with a version, but got %v", reqsWithVersion)
	}
}

func TestNonCMSRequestsAreProcessed(t *testing.T) {
	req, err := tryWriteRequest("http://localhost/some/other/service")

	if err != nil {
		t.Fatalf("Unable to write request: %v", err)
	}

	if req.Host != "localhost" {
		t.Fatalf("Expected host localhost but got %s", req.Host)
	}
}

func TestCMSRequestsForOtherPlatformsAreFiltered(t *testing.T) {
	urls := []string{
		"http://localhost/cms/mobile/v3/blackberry/json/latestfeed.json",
		"http://localhost/cms/partners/microsoft/windows8/v2/json/automobiles.json"}

	for _, url := range urls {
		_, err := tryWriteRequest(url)

		if err != io.EOF {
			t.Fatalf("Expected EOF file error for %s but got %v", url, err)
		}
	}
}

func TestRequestSchemeIsMaintained(t *testing.T) {
	req, err := tryWriteRequest("http://localhost/some/request")

	if err != nil {
		t.Errorf("Unable to write request: %v", err)
	}

	if req.URL.Scheme != "http" {
		t.Fatalf("Expected scheme http, but got %s", req.URL.Scheme)
	}
}
