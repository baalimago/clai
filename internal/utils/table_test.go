package utils

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_parseNumbersFromString(t *testing.T) {
	max := 5
	tests := []struct {
		name string
		in   string
		max  int
		want []int
	}{
		{"empty", "", max, []int{}},
		{"single", "3", max, []int{3}},
		{"multi", "1,3,5", max, []int{1, 3, 5}},
		{"spaces", " 1 ,  3 , 5 ", max, []int{1, 3, 5}},
		{"range", "2:4", max, []int{2, 3, 4}},
		{"range equal", "3:3", max, []int{3}},
		{"range and nums", "1,2:4,5", max, []int{1, 2, 3, 4, 5}},
		{"over max single", "7,2", max, []int{2}},
		{"over max range", "4:7", max, []int{4, 5}},
		{"invalid tok", "a,1,b", max, []int{1}},
		{"invalid range", "5:2", max, []int{}},
		{"partial bad range", "a:3,2", max, []int{2}},
		{"negatives kept", "-1,0,1", max, []int{-1, 0, 1}},
		{"dups kept", "1,1,2", max, []int{1, 1, 2}},
		{"spaces in range", "3 : 5", 7, []int{3, 4, 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseNumbersFromString(tt.in, tt.max)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_printSelectRow_success(t *testing.T) {
	var buf bytes.Buffer
	items := []string{"a", "b", "c"}
	format := func(i int, s string) (string, error) {
		return fmt.Sprintf("%d-%s", i, s), nil
	}
	err := printSelectRow(&buf, 1, items, format)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	want := "1-b\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func Test_printSelectRow_format_error(t *testing.T) {
	var buf bytes.Buffer
	items := []int{1, 2}
	format := func(i int, v int) (string, error) {
		return "", fmt.Errorf("boom")
	}
	err := printSelectRow(&buf, 0, items, format)
	if err == nil {
		t.Fatalf("expected error")
	}
	if buf.Len() != 0 {
		t.Errorf("expected no write, got %q", buf.String())
	}
}

func Test_printSelectItemOptions_first_page(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	format := func(i int, s string) (string, error) {
		return fmt.Sprintf("%d-%s", i, s), nil
	}
	var am int
	out := testboil.CaptureStdout(t, func(t *testing.T) {
		n, err := printSelectItemOptions(

			0, 3, len(items), items, "[%d/%d]\n", format,
		)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		am = n
	})
	if am != 3 {
		t.Fatalf("amPrinted=%d, want 3", am)
	}
	want := strings.Join(
		[]string{"0-a", "1-b", "2-c", "[0/1]"},
		"\n",
	) + "\n"
	if out != want {
		t.Fatalf("out=%q, want %q", out, want)
	}
}

func Test_printSelectItemOptions_last_partial_page(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	format := func(i int, s string) (string, error) {
		return fmt.Sprintf("%d-%s", i, s), nil
	}
	var am int
	out := testboil.CaptureStdout(t, func(t *testing.T) {
		n, err := printSelectItemOptions[string](
			1, 3, len(items), items, "[%d/%d]\n", format,
		)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		am = n
	})
	if am != 2 {
		t.Fatalf("amPrinted=%d, want 2", am)
	}
	want := strings.Join(
		[]string{"3-d", "4-e", "[1/1]"},
		"\n",
	) + "\n"
	if out != want {
		t.Fatalf("out=%q, want %q", out, want)
	}
}

func Test_printSelectItemOptions_format_error(t *testing.T) {
	items := []int{1, 2, 3}
	format := func(i int, v int) (string, error) {
		return "", fmt.Errorf("boom at %d", i)
	}
	out := testboil.CaptureStdout(t, func(t *testing.T) {
		_, err := printSelectItemOptions(
			0, 5, len(items), items, "[%d/%d]\n", format,
		)
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "printRow") {
			t.Fatalf("err=%v", err)
		}
	})
	if out != "" {
		t.Fatalf("expected no output, got %q", out)
	}
}

func Test_printSelectItemOptions_empty_items(t *testing.T) {
	items := []string{}
	format := func(i int, s string) (string, error) {
		return fmt.Sprintf("%d-%s", i, s), nil
	}
	var am int
	out := testboil.CaptureStdout(t, func(t *testing.T) {
		n, err := printSelectItemOptions(
			0, 3, len(items), items, "[%d/%d]\n", format,
		)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		am = n
	})
	if am != 0 {
		t.Fatalf("amPrinted=%d, want 0", am)
	}
	want := "[0/0]\n"
	if out != want {
		t.Fatalf("out=%q, want %q", out, want)
	}
}
