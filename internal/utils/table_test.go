package utils

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type testPaginator struct {
	total   int
	items   []int
	findErr error
}

var _ Paginator[int] = testPaginator{}

func (tp testPaginator) totalAm() int {
	return tp.total
}

func (tp testPaginator) findPage(start, offset int) ([]int, error) {
	if tp.findErr != nil {
		return nil, tp.findErr
	}
	end := min(start+offset, len(tp.items))
	if start > len(tp.items) {
		return []int{}, nil
	}
	return tp.items[start:end], nil
}

func Test_table_nextPage(t *testing.T) {
	tab := table[int]{page: 1, lastPage: 2}

	action := tab.nextPage()
	if action.Format != "[n]ext" || action.Short != "n" || action.Long != "next" {
		t.Fatalf("nextPage() metadata = %+v", action)
	}
	if action.AdditionalHotkeys != "" {
		t.Fatalf("nextPage() AdditionalHotkeys = %q, want empty string", action.AdditionalHotkeys)
	}
	if err := action.Action(); err != nil {
		t.Fatalf("nextPage action returned error: %v", err)
	}
	if tab.page != 2 {
		t.Fatalf("page after nextPage = %d, want 2", tab.page)
	}

	if err := action.Action(); err != nil {
		t.Fatalf("nextPage wrap action returned error: %v", err)
	}
	if tab.page != 0 {
		t.Fatalf("page after nextPage wrap = %d, want 0", tab.page)
	}
}

func Test_table_prevPage(t *testing.T) {
	tab := table[int]{page: 1, lastPage: 2}

	action := tab.prevPage()
	if action.Format != "[p]rev" || action.Short != "p" || action.Long != "prev" {
		t.Fatalf("prevPage() metadata = %+v", action)
	}
	if err := action.Action(); err != nil {
		t.Fatalf("prevPage action returned error: %v", err)
	}
	if tab.page != 0 {
		t.Fatalf("page after prevPage = %d, want 0", tab.page)
	}

	if err := action.Action(); err != nil {
		t.Fatalf("prevPage wrap action returned error: %v", err)
	}
	if tab.page != 2 {
		t.Fatalf("page after prevPage wrap = %d, want 2", tab.page)
	}
}

func Test_table_quit(t *testing.T) {
	action := new(table[int]).quit()
	if action.Format != "[q]uit" || action.Short != "q" || action.Long != "quit" {
		t.Fatalf("quit() metadata = %+v", action)
	}
	err := action.Action()
	if !errors.Is(err, ErrUserInitiatedExit) {
		t.Fatalf("quit action error = %v, want %v", err, ErrUserInitiatedExit)
	}
}

func Test_table_back(t *testing.T) {
	action := new(table[int]).back()
	if action.Format != "[b]ack" || action.Short != "b" || action.Long != "back" {
		t.Fatalf("back() metadata = %+v", action)
	}
	err := action.Action()
	if !errors.Is(err, ErrBack) {
		t.Fatalf("back action error = %v, want %v", err, ErrBack)
	}
}

func Test_table_pageCount(t *testing.T) {
	tests := []struct {
		name     string
		pageSize int
		total    int
		want     int
	}{
		{name: "zero page size returns zero", pageSize: 0, total: 10, want: 0},
		{name: "negative page size returns zero", pageSize: -1, total: 10, want: 0},
		{name: "zero total returns zero", pageSize: 5, total: 0, want: 0},
		{name: "single page returns zero", pageSize: 5, total: 5, want: 0},
		{name: "partial last page rounds down to last page index", pageSize: 5, total: 6, want: 1},
		{name: "multiple full pages returns last page index", pageSize: 5, total: 15, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tab := table[int]{
				pageSize:  tt.pageSize,
				paginator: testPaginator{total: tt.total},
			}

			got := tab.pageCount()
			if got != tt.want {
				t.Errorf("pageCount() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_table_tableActionsString(t *testing.T) {
	tests := []struct {
		name    string
		actions []TableAction
		want    string
	}{
		{name: "no actions returns empty string", actions: nil, want: ""},
		{name: "single action returns its format", actions: []TableAction{{Format: "[n]ext"}}, want: "[n]ext"},
		{
			name:    "multiple actions are comma separated in order",
			actions: []TableAction{{Format: "[p]rev"}, {Format: "[n]ext"}, {Format: "[q]uit"}},
			want:    "[p]rev, [n]ext, [q]uit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tab := table[int]{tableActions: tt.actions}
			got := tab.tableActionsString()
			if got != tt.want {
				t.Fatalf("tableActionsString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func Test_table_multiPartParse(t *testing.T) {
	tests := []struct {
		name    string
		total   int
		input   string
		want    []int
		wantErr string
	}{
		{name: "missing colon returns error", total: 10, input: "3", wantErr: "expected 2 numbers from range"},
		{name: "too many colons returns error", total: 10, input: "1:2:3", wantErr: "expected 2 numbers from range"},
		{name: "invalid start returns error", total: 10, input: "a:2", wantErr: "failed to parse start"},
		{name: "invalid end returns error", total: 10, input: "1:b", wantErr: "failed to parse end"},
		{name: "end before start returns error", total: 10, input: "4:2", wantErr: "start of range: 4, is greater than end: 2"},
		{name: "single value range returns inclusive selection", total: 10, input: "2:2", want: []int{2}},
		{name: "whitespace is trimmed", total: 10, input: " 1 : 3 ", want: []int{1, 2, 3}},
		{name: "range beyond total truncates", total: 3, input: "2:5", want: []int{2, 3}},
		{name: "range stopping immediately beyond total returns empty", total: 0, input: "1:3", want: []int{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tab := table[int]{paginator: testPaginator{total: tt.total}}
			got, err := tab.multiPartParse(tt.input)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("multiPartParse(%q) error = nil, want substring %q", tt.input, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("multiPartParse(%q) error = %q, want substring %q", tt.input, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("multiPartParse(%q) unexpected error: %v", tt.input, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("multiPartParse(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func Test_table_parseNumbersFromString(t *testing.T) {
	tests := []struct {
		name    string
		total   int
		input   string
		want    []int
		wantErr []string
	}{
		{name: "parses comma separated integers", total: 10, input: "1, 3,5", want: []int{1, 3, 5}},
		{name: "parses ranges without integer parse noise", total: 10, input: "1:3", want: []int{1, 2, 3}},
		{name: "parses mixed integers and ranges in order", total: 10, input: "0, 2:4, 6", want: []int{0, 2, 3, 4, 6}},
		{name: "truncates ranges at total amount", total: 3, input: "2:5", want: []int{2, 3}},
		{name: "keeps valid selections when one token is invalid", total: 10, input: "1, nope, 4", want: []int{1, 4}, wantErr: []string{"token: 'nope' failed to parse to int"}},
		{name: "reports out of bounds singular values", total: 3, input: "1,4", want: []int{1}, wantErr: []string{"index: '4' is higher than max amount of items"}},
		{name: "reports malformed ranges without adding values", total: 10, input: "1:bad", want: []int{}, wantErr: []string{"failed to parse range selection: failed to parse end"}},
		{name: "empty token is reported", total: 3, input: "1,,2", want: []int{1, 2}, wantErr: []string{"token: '' failed to parse to int"}},
		{
			name:  "joins multiple parse errors and preserves valid selections",
			total: 3,
			input: "bad, 1:broken, 4, 2",
			want:  []int{2},
			wantErr: []string{
				"token: 'bad' failed to parse to int",
				"failed to parse range selection: failed to parse end",
				"index: '4' is higher than max amount of items",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tab := table[int]{paginator: testPaginator{total: tt.total}}
			got, err := tab.parseNumbersFromString(tt.input)

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseNumbersFromString(%q) = %v, want %v", tt.input, got, tt.want)
			}

			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("parseNumbersFromString(%q) unexpected error: %v", tt.input, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("parseNumbersFromString(%q) error = nil, want substrings %v", tt.input, tt.wantErr)
			}
			for _, wantErr := range tt.wantErr {
				if !strings.Contains(err.Error(), wantErr) {
					t.Fatalf("parseNumbersFromString(%q) error = %q, want substring %q", tt.input, err.Error(), wantErr)
				}
			}
		})
	}
}

func Test_table_printRow(t *testing.T) {
	t.Run("formats and writes row", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")

		var out bytes.Buffer
		tab := table[int]{
			rowFormater: func(i, item int) (string, error) {
				return fmt.Sprintf("%d=%d", i, item), nil
			},
			out: &out,
		}

		if err := tab.printRow(2, 42); err != nil {
			t.Fatalf("printRow() unexpected error: %v", err)
		}
		if got := out.String(); got != "2=42\n" {
			t.Fatalf("printRow() output = %q, want %q", got, "2=42\n")
		}
	})

	t.Run("formatter error is wrapped", func(t *testing.T) {
		tab := table[int]{
			rowFormater: func(i, item int) (string, error) {
				return "", errors.New("boom")
			},
			out: new(bytes.Buffer),
		}

		err := tab.printRow(0, 1)
		if err == nil {
			t.Fatal("printRow() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "failed to format row") {
			t.Fatalf("printRow() error = %q, want format context", err.Error())
		}
	})

	t.Run("writer error is wrapped", func(t *testing.T) {
		tab := table[int]{
			rowFormater: func(i, item int) (string, error) {
				return "row", nil
			},
			out: errWriter{},
		}

		err := tab.printRow(0, 1)
		if err == nil {
			t.Fatal("printRow() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "failed to print") {
			t.Fatalf("printRow() error = %q, want print context", err.Error())
		}
	})
}

func Test_table_print(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	t.Run("prints current page and prompt", func(t *testing.T) {
		var out bytes.Buffer
		tab := table[int]{
			page:         1,
			pageSize:     2,
			lastPage:     2,
			paginator:    testPaginator{total: 5, items: []int{10, 20, 30, 40, 50}},
			rowFormater:  func(i, item int) (string, error) { return fmt.Sprintf("%d=%d", i, item), nil },
			tableActions: []TableAction{{Format: "[n]ext"}, {Format: "[q]uit"}},
			out:          &out,
		}

		got, err := tab.print()
		if err != nil {
			t.Fatalf("print() unexpected error: %v", err)
		}
		if got != 2 {
			t.Fatalf("print() printed = %d, want 2", got)
		}

		wantContains := []string{"2=30\n", "3=40\n", "select ([n]ext, [q]uit, page 1/2): "}
		for _, want := range wantContains {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("print() output = %q, want substring %q", out.String(), want)
			}
		}
	})

	t.Run("findPage error is wrapped", func(t *testing.T) {
		tab := table[int]{
			pageSize:    1,
			paginator:   testPaginator{total: 1, findErr: errors.New("boom")},
			rowFormater: func(i, item int) (string, error) { return "", nil },
			out:         new(bytes.Buffer),
		}

		_, err := tab.print()
		if err == nil {
			t.Fatal("print() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "failed to find page") {
			t.Fatalf("print() error = %q, want findPage context", err.Error())
		}
	})

	t.Run("row printing error is wrapped", func(t *testing.T) {
		tab := table[int]{
			pageSize:    1,
			paginator:   testPaginator{total: 1, items: []int{10}},
			rowFormater: func(i, item int) (string, error) { return "", errors.New("boom") },
			out:         new(bytes.Buffer),
		}

		_, err := tab.print()
		if err == nil {
			t.Fatal("print() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "failed to print row") {
			t.Fatalf("print() error = %q, want row-print context", err.Error())
		}
	})
}

func Test_table_selectNumbers(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("COLUMNS", "5")

	tests := []struct {
		name      string
		input     string
		actions   []TableAction
		want      []int
		wantErr   string
		wantErrIs error
		wantNil   bool
		wantPage  int
	}{
		{
			name:     "returns parsed numbers",
			input:    "1,2\n",
			want:     []int{1, 2},
			wantPage: 0,
		},
		{
			name:  "matches short action",
			input: "n\n",
			actions: []TableAction{{
				Format: "[n]ext",
				Short:  "n",
				Long:   "next",
				Action: func() error { return nil },
			}},
			wantNil: true,
		},
		{
			name:  "matches long action",
			input: "next\n",
			actions: []TableAction{{
				Format: "[n]ext",
				Short:  "n",
				Long:   "next",
				Action: func() error { return nil },
			}},
			wantNil: true,
		},
		{
			name:  "matches additional hotkey",
			input: "\n",
			actions: []TableAction{{
				Format:            "[n]ext",
				Short:             "n",
				Long:              "next",
				AdditionalHotkeys: "",
				Action:            func() error { return nil },
			}},
			wantNil: true,
		},
		{
			name:  "nil action errors",
			input: "next\n",
			actions: []TableAction{{
				Format: "[n]ext",
				Short:  "n",
				Long:   "next",
			}},
			wantErr: `table action "next" has nil action`,
		},
		{
			name:      "action sentinel propagates",
			input:     "quit\n",
			actions:   []TableAction{{Format: "[q]uit", Short: "q", Long: "quit", Action: func() error { return ErrUserInitiatedExit }}},
			wantErrIs: ErrUserInitiatedExit,
		},
		{
			name:    "parse error returns selected numbers and wrapped error",
			input:   "1,bad\n",
			want:    []int{1},
			wantErr: `failed to parse selected numbers from choice "1,bad"`,
		},
		{
			name:  "action can mutate table page",
			input: "next\n",
			actions: []TableAction{{
				Format: "[n]ext",
				Short:  "n",
				Long:   "next",
			}},
			wantNil:  true,
			wantPage: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ttyPath := filepath.Join(t.TempDir(), "tty")
			if err := os.WriteFile(ttyPath, []byte(tt.input), 0o600); err != nil {
				t.Fatalf("write tty input: %v", err)
			}
			t.Setenv("TTY", ttyPath)

			var out bytes.Buffer
			tab := table[int]{
				page:         0,
				pageSize:     3,
				lastPage:     1,
				paginator:    testPaginator{total: 3, items: []int{10, 20, 30}},
				rowFormater:  func(i, item int) (string, error) { return fmt.Sprintf("%d=%d", i, item), nil },
				tableActions: tt.actions,
				out:          &out,
			}
			if tt.name == "action can mutate table page" {
				tab.tableActions[0] = tab.nextPage()
			}

			got, err := tab.selectNumbers()

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("selectNumbers() error = nil, want substring %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("selectNumbers() error = %q, want substring %q", err.Error(), tt.wantErr)
				}
			} else if tt.wantErrIs != nil {
				if !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("selectNumbers() error = %v, want %v", err, tt.wantErrIs)
				}
			} else if err != nil {
				t.Fatalf("selectNumbers() unexpected error: %v", err)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("selectNumbers() = %v, want %v", got, tt.want)
			}
			if tt.wantNil && got != nil {
				t.Fatalf("selectNumbers() = %v, want nil selection", got)
			}
			if tab.page != tt.wantPage {
				t.Fatalf("page after selectNumbers() = %d, want %d", tab.page, tt.wantPage)
			}
			if out.Len() == 0 {
				t.Fatal("selectNumbers() wrote no table output")
			}
		})
	}
}

func Test_table_selectNumbers_readError(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TTY", filepath.Join(t.TempDir(), "missing-tty"))

	tab := table[int]{
		pageSize:    1,
		paginator:   testPaginator{total: 1, items: []int{10}},
		rowFormater: func(i, item int) (string, error) { return fmt.Sprintf("%d=%d", i, item), nil },
		out:         new(bytes.Buffer),
	}

	_, err := tab.selectNumbers()
	if err == nil {
		t.Fatal("selectNumbers() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "failed to read table selection") {
		t.Fatalf("selectNumbers() error = %q, want read context", err.Error())
	}
}

func Test_table_selectNumbers_printError(t *testing.T) {
	tab := table[int]{
		pageSize:    1,
		paginator:   testPaginator{total: 1, findErr: errors.New("boom")},
		rowFormater: func(i, item int) (string, error) { return "", nil },
		out:         new(bytes.Buffer),
	}

	_, err := tab.selectNumbers()
	if err == nil {
		t.Fatal("selectNumbers() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "failed to print table") {
		t.Fatalf("selectNumbers() error = %q, want print-table context", err.Error())
	}
}

func Test_SelectFromTable(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("COLUMNS", "5")

	t.Run("returns selected numbers", func(t *testing.T) {
		ttyPath := filepath.Join(t.TempDir(), "tty")
		if err := os.WriteFile(ttyPath, []byte("1\n"), 0o600); err != nil {
			t.Fatalf("write tty input: %v", err)
		}
		t.Setenv("TTY", ttyPath)

		got, err := SelectFromTable(
			"header",
			testPaginator{total: 2, items: []int{10, 20}},
			"",
			func(i, item int) (string, error) { return fmt.Sprintf("%d=%d", i, item), nil },
			2,
			false,
			nil,
			new(bytes.Buffer),
		)
		if err != nil {
			t.Fatalf("SelectFromTable() unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, []int{1}) {
			t.Fatalf("SelectFromTable() = %v, want [1]", got)
		}
	})

	t.Run("wraps selectNumbers error", func(t *testing.T) {
		restoreReadUserInput := UseReadUserInputForTests(func() (string, error) {
			return "x", nil
		})
		defer restoreReadUserInput()

		_, err := SelectFromTable(
			"header",
			testPaginator{total: 1, items: []int{10}},
			"",
			func(i, item int) (string, error) { return fmt.Sprintf("%d=%d", i, item), nil },
			1,
			false,
			[]TableAction{{Format: "[x]tra", Short: "x", Long: "extra", Action: nil}},
			new(bytes.Buffer),
		)
		if err == nil {
			t.Fatal("SelectFromTable() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "failed to select number") {
			t.Fatalf("SelectFromTable() error = %q, want selection context", err.Error())
		}
	})

	t.Run("rejects multiple selections when only one is allowed", func(t *testing.T) {
		ttyPath := filepath.Join(t.TempDir(), "tty")
		if err := os.WriteFile(ttyPath, []byte("0,1\n"), 0o600); err != nil {
			t.Fatalf("write tty input: %v", err)
		}
		t.Setenv("TTY", ttyPath)

		got, err := SelectFromTable(
			"header",
			testPaginator{total: 2, items: []int{10, 20}},
			"",
			func(i, item int) (string, error) { return fmt.Sprintf("%d=%d", i, item), nil },
			2,
			true,
			nil,
			new(bytes.Buffer),
		)
		if err == nil {
			t.Fatal("SelectFromTable() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "only one selected number supported") {
			t.Fatalf("SelectFromTable() error = %q, want only-one context", err.Error())
		}
		if len(got) != 0 {
			t.Fatalf("SelectFromTable() got = %v, want empty selection", got)
		}
	})

	t.Run("includes default actions so back works", func(t *testing.T) {
		ttyPath := filepath.Join(t.TempDir(), "tty")
		if err := os.WriteFile(ttyPath, []byte("b\n"), 0o600); err != nil {
			t.Fatalf("write tty input: %v", err)
		}
		t.Setenv("TTY", ttyPath)

		_, err := SelectFromTable(
			"header",
			testPaginator{total: 1, items: []int{10}},
			"unused",
			func(i, item int) (string, error) { return fmt.Sprintf("%d=%d", i, item), nil },
			1,
			false,
			[]TableAction{{Format: "[x]tra", Short: "x", Long: "extra", Action: func() error { return nil }}},
			new(bytes.Buffer),
		)
		if !errors.Is(err, ErrBack) {
			t.Fatalf("SelectFromTable() error = %v, want %v", err, ErrBack)
		}
	})

	t.Run("empty input advances to next page end to end", func(t *testing.T) {
		var out bytes.Buffer
		inputs := []string{"", "1"}
		restoreReadUserInput := UseReadUserInputForTests(func() (string, error) {
			if len(inputs) == 0 {
				return "", fmt.Errorf("no more test inputs")
			}
			next := inputs[0]
			inputs = inputs[1:]
			return next, nil
		})
		defer restoreReadUserInput()

		got, err := SelectFromTable(
			"header",
			testPaginator{total: 4, items: []int{10, 20, 30, 40}},
			"",
			func(i, item int) (string, error) { return fmt.Sprintf("%d=%d", i, item), nil },
			2,
			true,
			nil,
			&out,
		)
		if err != nil {
			t.Fatalf("SelectFromTable() unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, []int{1}) {
			t.Fatalf("SelectFromTable() = %v, want [1]", got)
		}

		output := out.String()
		for _, want := range []string{"0=10\n", "1=20\n", "2=30\n", "3=40\n", "page 0/1", "page 1/1"} {
			if !strings.Contains(output, want) {
				t.Fatalf("SelectFromTable() output = %q, want substring %q", output, want)
			}
		}
	})

	t.Run("clears header separator rows too", func(t *testing.T) {
		originalClearTermTo := clearTermToFn
		defer func() { clearTermToFn = originalClearTermTo }()

		var cleared []int
		clearTermToFn = func(w io.Writer, termWidth, upTo int) error {
			cleared = append(cleared, upTo)
			return nil
		}

		inputs := []string{"0"}
		restoreReadUserInput := UseReadUserInputForTests(func() (string, error) {
			if len(inputs) == 0 {
				return "", fmt.Errorf("no more test inputs")
			}
			next := inputs[0]
			inputs = inputs[1:]
			return next, nil
		})
		defer restoreReadUserInput()

		if _, err := SelectFromTable(
			"header",
			testPaginator{total: 1, items: []int{10}},
			"",
			func(i, item int) (string, error) { return fmt.Sprintf("%d=%d", i, item), nil },
			1,
			true,
			nil,
			new(bytes.Buffer),
		); err != nil {
			t.Fatalf("SelectFromTable() unexpected error: %v", err)
		}

		if !reflect.DeepEqual(cleared, []int{2, 2}) {
			t.Fatalf("ClearTermTo() calls = %v, want [2 2]", cleared)
		}
		totalCleared := 0
		for _, clearedRows := range cleared {
			totalCleared += clearedRows
		}
		if totalCleared != 4 {
			t.Fatalf("total cleared rows = %d, want 4", totalCleared)
		}
	})

	t.Run("selection type suppresses duplicate built in actions", func(t *testing.T) {
		t.Setenv("TTY", filepath.Join(t.TempDir(), "missing-tty"))

		var out bytes.Buffer
		tab := table[int]{
			pageSize:      1,
			paginator:     testPaginator{total: 1, items: []int{10}},
			rowFormater:   func(i, item int) (string, error) { return fmt.Sprintf("%d=%d", i, item), nil },
			selectionType: "goto chat: [<num>], next: [<enter>]/[n]ext, [p]rev, [q]uit",
			tableActions: []TableAction{
				{Format: "[p]rev", Short: "p", Long: "prev", Action: func() error { return nil }},
				{Format: "[n]ext", Short: "n", Long: "next", AdditionalHotkeys: "", Action: func() error { return nil }},
				{Format: "[b]ack", Short: "b", Long: "back", Action: func() error { return nil }},
				{Format: "[q]uit", Short: "q", Long: "quit", Action: func() error { return nil }},
			},
			out: &out,
		}

		if _, err := tab.print(); err != nil {
			t.Fatalf("print() unexpected error: %v", err)
		}

		got := out.String()
		if strings.Contains(got, "[p]rev, [n]ext, [b]ack, [q]uit") {
			t.Fatalf("print() output = %q, want built-in actions omitted from prompt", got)
		}
		if !strings.Contains(got, "goto chat: [<num>], next: [<enter>]/[n]ext, [p]rev, [q]uit") {
			t.Fatalf("print() output = %q, want custom selection type prompt", got)
		}
	})

	t.Run("does not print page indicator for single page", func(t *testing.T) {
		prevReadUserInput := readUserInputFn
		defer func() { readUserInputFn = prevReadUserInput }()
		readUserInputFn = func() (string, error) { return "0", nil }

		prevClear := clearTermToFn
		defer func() { clearTermToFn = prevClear }()
		clearTermToFn = func(io.Writer, int, int) error { return nil }

		var out bytes.Buffer
		got, err := SelectFromTable(
			"header",
			testPaginator{total: 1, items: []int{10}},
			"Select item <num>",
			func(i int, item int) (string, error) { return fmt.Sprintf("%d=%d", i, item), nil },
			10,
			true,
			nil,
			&out,
		)
		if err != nil {
			t.Fatalf("SelectFromTable() unexpected error: %v", err)
		}
		if !slices.Equal(got, []int{0}) {
			t.Fatalf("SelectFromTable() = %v, want [0]", got)
		}
		if strings.Contains(out.String(), "page ") {
			t.Fatalf("SelectFromTable() output = %q, want no page indicator", out.String())
		}
	})

	t.Run("returns error on duplicate table action hotkeys", func(t *testing.T) {
		prevReadUserInput := readUserInputFn
		defer func() { readUserInputFn = prevReadUserInput }()
		readUserInputFn = func() (string, error) { return "0", nil }

		prevClear := clearTermToFn
		defer func() { clearTermToFn = prevClear }()
		clearTermToFn = func(io.Writer, int, int) error { return nil }

		_, err := SelectFromTable(
			"header",
			testPaginator{total: 1, items: []int{10}},
			"Select item <num>",
			func(i int, item int) (string, error) { return fmt.Sprintf("%d=%d", i, item), nil },
			10,
			true,
			[]TableAction{{Format: "[n]ew", Short: "n", Long: "new", Action: func() error { return nil }}},
			io.Discard,
		)
		if err == nil {
			t.Fatal("SelectFromTable() error = nil, want duplicate hotkey error")
		}
		if !strings.Contains(err.Error(), `duplicate table action hotkey "n"`) {
			t.Fatalf("SelectFromTable() error = %q, want duplicate hotkey context", err.Error())
		}
	})

	t.Run("returns error on duplicate built-in and additional action even when identical", func(t *testing.T) {
		prevReadUserInput := readUserInputFn
		defer func() { readUserInputFn = prevReadUserInput }()
		readUserInputFn = func() (string, error) { return "0", nil }

		prevClear := clearTermToFn
		defer func() { clearTermToFn = prevClear }()
		clearTermToFn = func(io.Writer, int, int) error { return nil }

		_, err := SelectFromTable(
			"header",
			testPaginator{total: 1, items: []int{10}},
			"Select item <num>",
			func(i int, item int) (string, error) { return fmt.Sprintf("%d=%d", i, item), nil },
			10,
			true,
			[]TableAction{{Format: "[b]ack", Short: "b", Long: "back", Action: func() error { return nil }}},
			io.Discard,
		)
		if err == nil {
			t.Fatal("SelectFromTable() error = nil, want duplicate hotkey error")
		}
		if !strings.Contains(err.Error(), `duplicate table action hotkey "b"`) {
			t.Fatalf("SelectFromTable() error = %q, want duplicate hotkey context", err.Error())
		}
	})
}

func TestSlicePaginator(t *testing.T) {
	paginator := SlicePaginator([]int{10, 20, 30})

	got, err := paginator.findPage(1, 2)
	if err != nil {
		t.Fatalf("findPage() unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []int{20, 30}) {
		t.Fatalf("findPage() = %v, want %v", got, []int{20, 30})
	}
	if paginator.totalAm() != 3 {
		t.Fatalf("totalAm() = %d, want 3", paginator.totalAm())
	}

	_, err = paginator.findPage(-1, 1)
	if err == nil || !strings.Contains(err.Error(), "start index -1 below zero") {
		t.Fatalf("negative start error = %v, want wrapped bounds error", err)
	}

	_, err = paginator.findPage(0, -1)
	if err == nil || !strings.Contains(err.Error(), "offset -1 below zero") {
		t.Fatalf("negative offset error = %v, want wrapped bounds error", err)
	}

	got, err = paginator.findPage(99, 1)
	if err != nil {
		t.Fatalf("out-of-range page unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("out-of-range page = %v, want empty", got)
	}
}

type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write boom")
}
