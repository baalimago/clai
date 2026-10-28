package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

type TestData struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestCreateFile(t *testing.T) {
	filePath := "test_create.json"
	defer os.Remove(filePath)

	data := &TestData{Name: "John", Age: 30}
	err := CreateFile(filePath, data)
	if err != nil {
		t.Errorf("CreateFile failed: %v", err)
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("File not created: %v", err)
	}
}

func TestWriteFile(t *testing.T) {
	filePath := "test_write.json"
	defer os.Remove(filePath)

	data := &TestData{Name: "Alice", Age: 25}
	err := WriteFile(filePath, data)
	if err != nil {
		t.Errorf("WriteFile failed: %v", err)
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("File not written: %v", err)
	}
}

func TestReadAndUnmarshal(t *testing.T) {
	filePath := "test_read.json"
	defer os.Remove(filePath)

	expected := &TestData{Name: "Bob", Age: 35}
	err := CreateFile(filePath, expected)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	var actual TestData
	err = ReadAndUnmarshal(filePath, &actual)
	if err != nil {
		t.Errorf("ReadAndUnmarshal failed: %v", err)
	}

	if actual.Name != expected.Name || actual.Age != expected.Age {
		t.Errorf("ReadAndUnmarshal returned unexpected data: got %+v, want %+v", actual, expected)
	}
}

func TestReadAndUnmarshal_FileNotFound(t *testing.T) {
	filePath := "nonexistent.json"
	var data TestData
	err := ReadAndUnmarshal(filePath, &data)
	if err == nil {
		t.Error("ReadAndUnmarshal should have failed for non-existent file")
	}
}

func TestSaveBase64File_fallsBackToTheTempDir(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	var got string
	var err error
	// The fallback notice goes to the captured stream, not the test log.
	_ = testboil.CaptureStdout(t, func(t *testing.T) { got, err = SaveBase64File("fallback", missing, "aGk=", "txt") })
	if err != nil {
		t.Fatalf("SaveBase64File: %v", err)
	}
	if filepath.Dir(got) != os.TempDir() {
		t.Fatalf("fell back to %q, want a file in %q", got, os.TempDir())
	}
}
