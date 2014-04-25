package main

import (
	"flag"
	"os"
	"bufio"
	"log"
	"strings"
	"compress/gzip"
	"net/http"
	"io"
	"bytes"
	"io/ioutil"
	"regexp"
	"net/textproto"
)

var (
	inputUrlsPath      string
	inputRequestsPath  string
	outputRequestsPath string
	output             io.WriteCloser
)

func init() {
	flag.StringVar(&inputUrlsPath, "inUrls", "", "URLs input path (line separated)")
	flag.StringVar(&inputRequestsPath, "in", "", "Full requests input path")
	flag.StringVar(&outputRequestsPath, "out", "", "Full requests output path")
}

func main() {
	flag.Parse()

	if inputUrlsPath == "" && inputRequestsPath == "" {
		flag.Usage()
		os.Exit(1)
	}

	if outputRequestsPath == "" {
		flag.Usage()
		os.Exit(1)
	} else if outputRequestsPath == "-" {
		output = os.Stdout
	} else {
		file, err := os.Create(outputRequestsPath)
		if (err != nil) {
			log.Fatalf("Unable to open %s for writing: %v", outputRequestsPath, err)
		} else {
			output = file
		}
	}

	if inputRequestsPath != "" {
		reader := inputReader(inputRequestsPath)
		bufReader := bufio.NewReader(reader)
		req, err := http.ReadRequest(bufReader)
		for err == nil {
			// Need to make request read any body buffer before continuing
			body := new(bytes.Buffer)
			body.ReadFrom(req.Body)
			req.Body = ioutil.NopCloser(body)

			writeRequest(req, output)

			req, err = http.ReadRequest(bufReader)
		}

		if (err != io.EOF) {
			log.Fatalf("Premature error reading requests: %v", err)
		}

		defer reader.Close()
	} else {
		reader := inputReader(inputUrlsPath)
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			url := scanner.Text()
			req, err := http.NewRequest("GET", url, nil)
			if err != nil {
				log.Printf("Unable to parse url %s into request: %v", url, err)
			} else  {
				writeRequest(req, output)
			}
		}
		defer reader.Close()
	}

	defer output.Close()
}

var AndroidPlatforms []string = []string{"android", "android_tablet", "kindle_fire"}
var IOSPlatforms []string = []string{"iphone", "ipad"}
var AllPlatforms []string = append(AndroidPlatforms, IOSPlatforms...)

var cmsMobileURLMatcher *regexp.Regexp = regexp.MustCompile(`/cms/mobile/v(\d+)/([^/]+)/([^/]+)/(.+)\..+`)
var anyCMSURLMatcher *regexp.Regexp = regexp.MustCompile(`/cms/.+`)


func writeRequest(req *http.Request, out io.Writer) {
	if !isCMSRequest(req) || isInPlatforms(req, AllPlatforms) {
		processRequest(req)
		// use WriteProxy to force writing request URI with hostname and scheme
		req.WriteProxy(out)
	}
}

func isInPlatforms(req *http.Request, platforms []string) bool {
	matches := cmsMobileURLMatcher.FindStringSubmatch(req.URL.Path)
	return matches != nil && len(matches) >= 4 && exists(len(platforms), func(i int) bool { return platforms[i] == matches[3] })
}

func isCMSRequest(req *http.Request) bool {
	return anyCMSURLMatcher.MatchString(req.URL.Path)
}

func exists(limit int, predicate func(i int) bool) bool {
	for i := 0; i < limit; i++ {
		if predicate(i) {
			return true
		}
	}
	return false
}

func processRequest(req *http.Request) {
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("User-Agent", "gobench")

	makeV3Request(req)
	addVersionForAndroidInternational(req)
}

func makeV3Request(req *http.Request) {
	path := req.URL.Path
	matchIndexes := cmsMobileURLMatcher.FindStringSubmatchIndex(path)
	if matchIndexes != nil && len(matchIndexes) >= 4 {
		newPath := path[:matchIndexes[2]] + "3" + path[matchIndexes[3]:]
		req.URL.Path = newPath
	}
}

var NytAppVersionHeader string = textproto.CanonicalMIMEHeaderKey("NYT-App-Version")

var androidInternationalCount int = 0
var AndroidInternationalVersion string = "3.9"

func addVersionForAndroidInternational(req *http.Request) {
	if isInPlatforms(req, AndroidPlatforms) {
		androidInternationalCount += 1
		if androidInternationalCount % 5 != 0 {
			req.Header.Set(NytAppVersionHeader, AndroidInternationalVersion)
		} else {
			req.Header.Del(NytAppVersionHeader)
		}
	}
}

func inputReader(path string) io.ReadCloser {
	file, err := os.Open(path)

	if err != nil {
		log.Fatalf("Failed: %v", err)
	}


	if strings.HasSuffix(path, ".gz") {
		zhandle, err := gzip.NewReader(file)

		if err != nil {
			log.Fatalf("Failed to create gzip reader for file %s: %v", path, err)
		}

		return zhandle
	} else {
		return file
	}
}
