package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	call := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := run(args, path, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	call("list")
	call("add", "First")
	call("add", "Second")
	call("update", "1", "Updated")
	call("mark-in-progress", "1")
	call("mark-done", "2")
	for _, filter := range []string{"not-done", "in-progress"} {
		got := call("list", filter)
		if !strings.Contains(got, "Updated") || strings.Contains(got, "Second") {
			t.Fatal(got)
		}
	}
	if got := call("list", "done"); !strings.Contains(got, "Second") || strings.Contains(got, "Updated") {
		t.Fatal(got)
	}
	call("delete", "2")
	call("add", "Third")
	s, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tasks) != 2 || s.Tasks[1].ID != 3 {
		t.Fatalf("bad persisted state: %+v", s)
	}
	before, _ := os.ReadFile(path)
	for _, args := range [][]string{{"add", " "}, {"update", "1"}, {"delete", "0"}, {"delete", "abc"}, {"delete", "99"}, {"list", "bad"}, {"unknown"}} {
		if err := run(args, path, &bytes.Buffer{}); err == nil {
			t.Fatalf("expected error: %v", args)
		}
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("invalid input changed storage")
	}
}
func TestCorruptStorage(t *testing.T) {
	for _, data := range []string{"broken", "null", "{}", `{"nextId":1,"tasks":[{"id":1}]}`} {
		path := filepath.Join(t.TempDir(), "tasks.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := run([]string{"add", "New"}, path, &bytes.Buffer{}); err == nil {
			t.Fatal("expected error")
		}
		got, _ := os.ReadFile(path)
		if string(got) != data {
			t.Fatal("corrupt file overwritten")
		}
	}
}
