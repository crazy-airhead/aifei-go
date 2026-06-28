package damigen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleSrc = `package demo

import "context"

//dami:provider user
type UserService interface {
	GetUserID(ctx context.Context, name string) (int64, error)
	Fire(ctx context.Context, name string) error
	Count(ctx context.Context) (int, error)
}

type NotExported interface{} // no annotation → ignored
`

func TestGenerate(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "user.go"), []byte(sampleSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := Generate(src, out); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(out, "dami_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	checks := []string{
		"package demo",
		`"github.com/crazy-airhead/aifei-go/dami"`,
		"type UserServiceClient struct",
		"func NewUserServiceClient(bus *dami.Bus) *UserServiceClient",
		"func (c *UserServiceClient) GetUserID(ctx context.Context, name string) (int64, error)",
		`return dami.Call1[int64](c.Bus, ctx, "user.GetUserID", name)`,
		"func (c *UserServiceClient) Fire(ctx context.Context, name string) error",
		`return dami.Call0(c.Bus, ctx, "user.Fire", name)`,
		"func (c *UserServiceClient) Count(ctx context.Context) (int, error)",
		`return dami.Call1[int](c.Bus, ctx, "user.Count")`,
	}
	for _, c := range checks {
		if !strings.Contains(s, c) {
			t.Errorf("generated output missing %q\n--- generated ---\n%s", c, s)
		}
	}
}

func TestGenerateNoProviders(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "plain.go"), []byte("package demo\n\ntype Plain struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Generate(src, t.TempDir()); err == nil {
		t.Fatal("want error when no //dami:provider interfaces present")
	}
}

func TestGenerateBadReturn(t *testing.T) {
	src := t.TempDir()
	bad := `package demo

import "context"

//dami:provider user
type Bad interface {
	TwoNoError(ctx context.Context) (int, string) // second result must be error
}
`
	if err := os.WriteFile(filepath.Join(src, "bad.go"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Generate(src, t.TempDir()); err == nil {
		t.Fatal("want error for unsupported return shape")
	}
}
