package utils

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type TableAction struct {
	// Format for the the menu item to be printed. Include short and long Format. Example [b]ack
	Format string
	// Short name back -> "b"
	Short string
	// Long name "back"
	Long string
	// AdditionalHotkeys which will trigger action
	AdditionalHotkeys string
	Action            func() error
}

//lint:ignore U1000 interface methods are exercised through generic interface values
type Paginator[T any] interface {
	totalAm() int
	findPage(start, offset int) ([]T, error)
}

func SlicePaginator[T any](items []T) Paginator[T] {
	return paginatorFuncs[T]{
		totalFn: func() int {
			return len(items)
		},
		findFn: func(start, offset int) ([]T, error) {
			if start < 0 {
				return nil, fmt.Errorf("start index %d below zero", start)
			}
			if offset < 0 {
				return nil, fmt.Errorf("offset %d below zero", offset)
			}
			if start >= len(items) {
				return []T{}, nil
			}
			end := min(start+offset, len(items))
			return items[start:end], nil
		},
	}
}

//lint:ignore U1000 methods are used via the Paginator interface
type paginatorFuncs[T any] struct {
	totalFn func() int
	findFn  func(start, offset int) ([]T, error)
}

//lint:ignore U1000 used via interface dispatch
func (pf paginatorFuncs[T]) totalAm() int {
	return pf.totalFn()
}

//lint:ignore U1000 used via interface dispatch
func (pf paginatorFuncs[T]) findPage(start, offset int) ([]T, error) {
	return pf.findFn(start, offset)
}

type table[T any] struct {
	debug         bool
	page          int
	pageSize      int
	lastPage      int
	selectionType string
	paginator     Paginator[T]
	rowFormater   func(int, T) (string, error)
	tableActions  []TableAction
	out           io.Writer
}

var clearTermToFn = ClearTermTo

func (t *table[T]) nextPage() TableAction {
	return TableAction{
		Format:            "[n]ext",
		Short:             "n",
		Long:              "next",
		AdditionalHotkeys: "",
		Action: func() error {
			t.page++
			if t.page > t.lastPage {
				t.page = 0
			}
			return nil
		},
	}
}

func (t *table[T]) prevPage() TableAction {
	return TableAction{
		Format: "[p]rev",
		Short:  "p",
		Long:   "prev",
		Action: func() error {
			t.page--
			if t.page < 0 {
				t.page = t.lastPage
			}
			return nil
		},
	}
}

func (t *table[T]) quit() TableAction {
	return TableAction{
		Format: "[q]uit",
		Short:  "q",
		Long:   "quit",
		Action: func() error {
			return ErrUserInitiatedExit
		},
	}
}

func (t *table[T]) back() TableAction {
	return TableAction{
		Format: "[b]ack",
		Short:  "b",
		Long:   "back",
		Action: func() error {
			return ErrBack
		},
	}
}

// SelectFromTable by:
// 1. Listing rows according to rowFormater
// 2. Returning a list of chosen numbers
//
// Valid inputs:
//   - nr = int < len(items)
//   - nr,nr,nr - This selects multiple numbers
//   - nr:nr,nr,nr:nr - This selects two ranges of nr, as well as a singular nr
func SelectFromTable[T any](
	header string,
	paginator Paginator[T],
	selectionType string,
	rowFormater func(int, T) (string, error),
	pageSize int,
	onlyOneSelect bool,
	additionalTableActions []TableAction,
	out io.Writer,
) ([]int, error) {
	_ = selectionType
	if out == nil {
		out = io.Writer(io.Discard)
	}
	fmt.Fprintln(out, Colorize(ThemePrimaryColor(), header))
	headerWidth := visibleRuneCount(header)
	line := strings.Repeat("-", headerWidth)
	fmt.Fprintf(out, "%v\n", Colorize(ThemePrimaryColor(), line))

	tab := table[T]{
		page:          0,
		pageSize:      pageSize,
		lastPage:      0,
		selectionType: selectionType,
		paginator:     paginator,
		rowFormater:   rowFormater,
		tableActions:  additionalTableActions,
		out:           out,
	}
	tab.lastPage = tab.pageCount()
	baseActions := []TableAction{tab.prevPage(), tab.nextPage(), tab.back(), tab.quit()}
	if err := validateTableActions(additionalTableActions, baseActions); err != nil {
		return nil, fmt.Errorf("failed to validate table actions: %w", err)
	}
	defer func() {
		if err := clearTermToFn(out, -1, 2); err != nil && tab.debug {
			ancli.Errf("failed to clear header: %v", err)
		}
	}()
	tab.tableActions = append(tab.tableActions, baseActions...)
	var (
		selectedNumbers []int
		err             error
	)
	for {
		selectedNumbers, err = tab.selectNumbers()
		if err != nil {
			return nil, fmt.Errorf("failed to select number: %w", err)
		}
		if selectedNumbers != nil {
			break
		}
	}

	if onlyOneSelect && len(selectedNumbers) > 1 {
		return []int{}, fmt.Errorf("only one selected number supported. selected indices: %v", selectedNumbers)
	}

	return selectedNumbers, nil
}

func (t *table[T]) printRow(i int, item T) error {
	formatted, err := t.rowFormater(i, item)
	if err != nil {
		return fmt.Errorf("failed to format row: %w", err)
	}

	_, err = fmt.Fprintln(t.out, Colorize(ThemeBreadtextColor(), formatted))
	if err != nil {
		return fmt.Errorf("failed to print: %w", err)
	}
	return nil
}

func (t *table[T]) print() (int, error) {
	totalItems := t.paginator.totalAm()
	pageIndex := t.page * t.pageSize
	listToIndex := min(pageIndex+t.pageSize, totalItems)

	amPrinted := 0
	items, err := t.paginator.findPage(pageIndex, t.pageSize)
	if err != nil {
		return 0, fmt.Errorf("failed to find page with pageIndex: %v, pageSize: %v. Error was: %w", pageIndex, t.pageSize, err)
	}
	for rowIndex := pageIndex; rowIndex < listToIndex; rowIndex++ {
		printErr := t.printRow(rowIndex, items[rowIndex-pageIndex])
		if printErr != nil {
			return 0, fmt.Errorf("failed to print row: %w", printErr)
		}
		amPrinted++
	}
	_, err = fmt.Fprint(t.out, Colorize(ThemeSecondaryColor(), t.promptLine()))
	if err != nil {
		return 0, fmt.Errorf("failed to print prompt line: %w", err)
	}
	return amPrinted, nil
}

func (t *table[T]) promptLine() string {
	selection := selectionTypeOrDefault(t.selectionType)
	actions := t.tableActionsString()
	if t.pageCount() == 0 {
		return fmt.Sprintf("%s (%s): ", selection, actions)
	}
	return fmt.Sprintf("%s (%s, page %v/%v): ", selection, actions, t.page, t.pageCount())
}

func selectionTypeOrDefault(selectionType string) string {
	if strings.TrimSpace(selectionType) == "" {
		return "select"
	}
	return selectionType
}

func (t *table[T]) selectNumbers() ([]int, error) {
	// Print the table for display in terminal
	amPrinted, err := t.print()
	if err != nil {
		return nil, fmt.Errorf("failed to print table: %w", err)
	}

	// Clear the terminal to provide clean output
	defer func() {
		if err := clearTermToFn(t.out, -1, amPrinted+1); err != nil {
			if t.debug {
				ancli.Errf("failed to clear table: %v", err)
			}
		}
	}()

	// Read user input to find out what to do next
	choice, err := ReadUserInput()
	if err != nil {
		return nil, fmt.Errorf("failed to read table selection: %w", err)
	}

	// See if the choice is a table action, if so, run it and return
	for _, action := range t.tableActions {
		additionalHotkeyMatch := false
		if action.AdditionalHotkeys != "" || (action.Long == "next" && action.Short == "n") {
			additionalHotkeyMatch = slices.Contains(strings.Split(action.AdditionalHotkeys, ","), choice)
		}
		if choice == action.Long || choice == action.Short || additionalHotkeyMatch {
			if action.Action == nil {
				return nil, fmt.Errorf("table action %q has nil action", action.Long)
			}

			if actErr := action.Action(); actErr != nil {
				return nil, actErr
			}
			return nil, nil
		}
	}

	// Its not a table action: see if some index has been selected
	selectedNumbers, err := t.parseNumbersFromString(choice)
	if err != nil {
		return selectedNumbers, fmt.Errorf("failed to parse selected numbers from choice %q: %w", choice, err)
	}

	return selectedNumbers, nil
}

func (t *table[T]) tableActionsString() string {
	if len(t.tableActions) == 0 {
		return ""
	}
	sb := strings.Builder{}
	lowerSelectionType := strings.ToLower(t.selectionType)
	for _, ata := range t.tableActions {
		if actionAlreadyDescribed(lowerSelectionType, ata) {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(ata.Format)
	}
	return sb.String()
}

func actionAlreadyDescribed(selectionType string, action TableAction) bool {
	if selectionType == "" {
		return false
	}

	candidates := []string{action.Format}
	for _, candidate := range candidates {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		if strings.Contains(selectionType, candidate) {
			return true
		}
	}
	return false
}

func validateTableActions(additionalActions, baseActions []TableAction) error {
	seen := map[string]TableAction{}
	for _, action := range baseActions {
		for _, key := range tableActionKeys(action) {
			seen[key] = action
		}
	}
	for _, action := range additionalActions {
		for _, key := range tableActionKeys(action) {
			if existing, found := seen[key]; found {
				return fmt.Errorf("duplicate table action hotkey %q between %q and %q", key, existing.Long, action.Long)
			}
			seen[key] = action
		}
	}
	return nil
}

func tableActionKeys(action TableAction) []string {
	keys := []string{action.Short, action.Long}
	if action.AdditionalHotkeys != "" {
		keys = append(keys, strings.Split(action.AdditionalHotkeys, ",")...)
	}
	ret := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		ret = append(ret, key)
	}
	return ret
}

func (t *table[T]) pageCount() int {
	if t.pageSize <= 0 || t.paginator.totalAm() <= 0 {
		return 0
	}
	return (t.paginator.totalAm() - 1) / t.pageSize
}

func (t *table[T]) multiPartParse(maybeRange string) ([]int, error) {
	parts := strings.Split(maybeRange, ":")
	if len(parts) != 2 {
		return []int{}, fmt.Errorf("expected 2 numbers from range: '%v', got: %v", maybeRange, len(parts))
	}
	start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return []int{}, fmt.Errorf("failed to parse start: '%v', err: %w", parts[0], err)
	}
	end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return []int{}, fmt.Errorf("failed to parse end: '%v', err: %w", parts[1], err)
	}

	if end < start {
		return []int{}, fmt.Errorf("start of range: %v, is greater than end: %v", start, end)
	}
	selectedNumbers := make([]int, 0)
	for i := start; i <= end; i++ {
		// End on max am to provide easy way to clear all items
		if i > t.paginator.totalAm() {
			return selectedNumbers, nil
		}
		selectedNumbers = append(selectedNumbers, i)
	}
	return selectedNumbers, nil
}

func (t *table[T]) parseNumbersFromString(choice string) ([]int, error) {
	selectedNumbers := make([]int, 0)
	parseErrors := make([]error, 0)
	tokens := strings.SplitSeq(choice, ",")
	for tok := range tokens {
		tok = strings.TrimSpace(tok)
		if strings.Contains(tok, ":") {
			multiPartParseSelNum, err := t.multiPartParse(tok)
			if err != nil {
				parseErrors = append(parseErrors, fmt.Errorf("failed to parse range selection: %w", err))
				continue
			}
			selectedNumbers = append(selectedNumbers, multiPartParseSelNum...)
			continue
		}
		v, err := strconv.Atoi(tok)
		if err != nil {
			parseErrors = append(parseErrors, fmt.Errorf("token: '%v' failed to parse to int: %w", tok, err))
			continue
		}
		if v > t.paginator.totalAm() {
			parseErrors = append(parseErrors, fmt.Errorf("index: '%v' is higher than max amount of items", v))
			continue
		}
		selectedNumbers = append(selectedNumbers, v)
	}

	return selectedNumbers, errors.Join(parseErrors...)
}
