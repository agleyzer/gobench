package main

import (
	"io/ioutil"
	"syscall"
	"testing"
)

func test_reader(t *testing.T, file string, expected []string) {
	f, err := ioutil.TempFile("", "testcontent")
	if err != nil {
		panic(err)
	}
	defer syscall.Unlink(f.Name())
	ioutil.WriteFile(f.Name(), []byte(file), 0644)

	r := NewInfiniteLineReader(f.Name())
	defer r.Close()

	for _, e := range expected {
		l := r.NextLine()

		if l != e {
			t.Fatalf("Expected [%s] but got [%s]", e, l)
		}
	}
}

func TestNoTrailingNl(t *testing.T) {
	test_reader(t,
		"foo\nbar\nbaz",
		[]string{"foo", "bar", "baz", "foo", "bar", "baz"})
}

func TestRegular(t *testing.T) {
	test_reader(t,
		"foo\nbar\nbaz\n",
		[]string{"foo", "bar", "baz", "foo", "bar", "baz"})
}
