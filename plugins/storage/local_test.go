package storage

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalRoundTrip(t *testing.T) {
	c, err := NewLocalClient("bucket", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewLocalClient: %v", err)
	}

	// Put with a nested key.
	pr, err := c.Put("a/b.txt", OfString("hello world", "text/plain"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if pr.Driver != "local" || pr.Bucket != "bucket" || pr.Key != "a/b.txt" {
		t.Fatalf("PutResult = %+v", pr)
	}
	if pr.Parent == "" {
		t.Fatal("PutResult.Parent empty")
	}

	// Exists.
	ok, err := c.Exists("a/b.txt")
	if err != nil || !ok {
		t.Fatalf("Exists a/b.txt = (%v,%v), want (true,nil)", ok, err)
	}
	ok, err = c.Exists("missing.txt")
	if err != nil || ok {
		t.Fatalf("Exists missing = (%v,%v), want (false,nil)", ok, err)
	}

	// Get.
	m, err := c.Get("a/b.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m == nil {
		t.Fatal("Get returned nil Media")
	}
	defer m.Close()
	if m.Size() != int64(len("hello world")) {
		t.Fatalf("Size = %d, want %d", m.Size(), len("hello world"))
	}
	s, _ := m.String()
	if s != "hello world" {
		t.Fatalf("content = %q", s)
	}

	// Get missing -> (nil, nil).
	m2, err := c.Get("nope.txt")
	if err != nil || m2 != nil {
		t.Fatalf("Get missing = (%v,%v), want (nil,nil)", m2, err)
	}

	// TempURL unsupported.
	if _, err := c.TempURL("a/b.txt", time.Minute); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("TempURL err = %v, want ErrUnsupported", err)
	}
}

func TestLocalDeleteAndBatch(t *testing.T) {
	c, _ := NewLocalClient("bucket", t.TempDir(), nil)
	if _, err := c.Put("x.txt", OfString("1", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Put("y.txt", OfString("2", "")); err != nil {
		t.Fatal(err)
	}

	if err := c.Delete("x.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if ok, _ := c.Exists("x.txt"); ok {
		t.Fatal("x.txt still exists after Delete")
	}
	// Deleting a missing object is not an error.
	if err := c.Delete("x.txt"); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}

	res, err := c.DeleteBatch([]string{"y.txt", "z.txt"})
	if err != nil {
		t.Fatalf("DeleteBatch: %v", err)
	}
	if res.Partial {
		t.Fatalf("DeleteBatch unexpected partial: %+v", res.Errors)
	}
	if ok, _ := c.Exists("y.txt"); ok {
		t.Fatal("y.txt still exists after DeleteBatch")
	}
}

func TestLocalPutResultParentAbsolute(t *testing.T) {
	root := t.TempDir()
	c, _ := NewLocalClient("bucket", root, nil)
	pr, err := c.Put("c.txt", OfString("x", ""))
	if err != nil {
		t.Fatal(err)
	}
	wantParent, _ := filepath.Abs(filepath.Join(root, "bucket"))
	if pr.Parent != wantParent {
		t.Fatalf("Parent = %q, want %q", pr.Parent, wantParent)
	}
}
