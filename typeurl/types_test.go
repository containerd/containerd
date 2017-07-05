package typeurl

import (
	"path/filepath"
	"reflect"
	"testing"
)

type test struct {
	Name string
	Age  int
}

func clear() {
	registry = make(map[reflect.Type]string)
}

func TestRegsiterValueGetValue(t *testing.T) {
	clear()
	expected := filepath.Join(Namespace, "test")
	Register(test{}, "test")

	url, err := TypeUrl(test{})
	if err != nil {
		t.Fatal(err)
	}
	if url != expected {
		t.Fatalf("expected %q but received %q", expected, url)
	}
}

func TestRegsiterValueGetPointer(t *testing.T) {
	clear()
	expected := filepath.Join(Namespace, "test")
	Register(test{}, "test")

	url, err := TypeUrl(&test{})
	if err != nil {
		t.Fatal(err)
	}
	if url != expected {
		t.Fatalf("expected %q but received %q", expected, url)
	}
}

func TestRegsiterPointerGetPointer(t *testing.T) {
	clear()
	expected := filepath.Join(Namespace, "test")
	Register(&test{}, "test")

	url, err := TypeUrl(&test{})
	if err != nil {
		t.Fatal(err)
	}
	if url != expected {
		t.Fatalf("expected %q but received %q", expected, url)
	}
}

func TestRegsiterPointerGetValue(t *testing.T) {
	clear()
	expected := filepath.Join(Namespace, "test")
	Register(&test{}, "test")

	url, err := TypeUrl(test{})
	if err != nil {
		t.Fatal(err)
	}
	if url != expected {
		t.Fatalf("expected %q but received %q", expected, url)
	}
}

func TestMarshal(t *testing.T) {
	clear()
	expected := filepath.Join(Namespace, "test")
	Register(test{}, "test")

	v := &test{
		Name: "koye",
		Age:  6,
	}
	any, err := MarshalAny(v)
	if err != nil {
		t.Fatal(err)
	}
	if any.TypeUrl != expected {
		t.Fatalf("expected %q but received %q", expected, any.TypeUrl)
	}
}

func TestMarshalUnmarshal(t *testing.T) {
	clear()
	Register(test{}, "test")

	v := &test{
		Name: "koye",
		Age:  6,
	}
	any, err := MarshalAny(v)
	if err != nil {
		t.Fatal(err)
	}
	nv, err := UnmarshalAny(any)
	if err != nil {
		t.Fatal(err)
	}
	td, ok := nv.(*test)
	if !ok {
		t.Fatal("expected value to cast to *test")
	}
	if td.Name != "koye" {
		t.Fatal("invalid name")
	}
	if td.Age != 6 {
		t.Fatal("invalid age")
	}
}
