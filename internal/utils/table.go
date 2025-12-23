package utils

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// SelectFromTable by:
// 1. Listing rows according to rowFormater
// 2. Returning a list of chosen numbers
//
// Valid inputs:
//   - nr = int < len(items)
//   - nr,nr,nr - This selects multiple numbers
//   - nr:nr,nr,nr:nr - This selects two ranges of nr, as well as a singular nr
func SelectFromTable[T any](header string, items []T,
	choicesFormat string,
	rowFormater func(int, T) (string, error),
	pageSize int,
	onlyOneSelect bool,
) ([]int, error) {
	fmt.Println(header)
	headerWidth := utf8.RuneCount([]byte(header))
	line := strings.Repeat("-", headerWidth)
	fmt.Printf("%v\n", line)

	page := 0
	amItems := len(items)
	noNumberSelected := true
	selectedNumbers := []int{}
	amPrinted := 0
	for noNumberSelected {
		tmpAmPrinted, printErr := printSelectItemOptions(page,
			pageSize,
			amItems,
			items,
			choicesFormat,
			rowFormater)
		if printErr != nil {
			return []int{}, fmt.Errorf("failed to printOptions: %w", printErr)
		}
		amPrinted = tmpAmPrinted
		choice, usrReadErr := ReadUserInput()
		if usrReadErr != nil {
			return []int{}, fmt.Errorf("conv list failed to read user: %w", usrReadErr)
		}
		goPrevPageChoices := []string{"p", "prev"}
		toClear := amPrinted + 1
		if slices.Contains(goPrevPageChoices, choice) {
			page--
			if page < 0 {
				page = amItems / pageSize
			}
		} else {
			selectedNumbers = parseNumbersFromString(choice, amItems)
			noNumberSelected = len(selectedNumbers) == 0
			if !noNumberSelected {
				// +2 in case of since we want to remove "---" line and header
				toClear += 2
				// Explicit break for clarity
				break
			}
			page++
			if page > amItems/pageSize {
				page = 0
			}
		}
		err := ClearTermTo(os.Stdout, -1, toClear)
		if err != nil {
			return []int{}, fmt.Errorf("failed to clear term: %w", err)
		}

	}

	if onlyOneSelect && len(selectedNumbers) > 1 {
		return []int{}, fmt.Errorf("only one selected number supported. Selected indices: '%v'", selectedNumbers)
	}

	return selectedNumbers, nil
}

func parseNumbersFromString(choice string, max int) []int {
	selectedNumbers := make([]int, 0)
	// check if matches pattern nr0:nr1, if yes select range
	tokens := strings.Split(choice, ",")
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if strings.Contains(tok, ":") {
			parts := strings.SplitN(tok, ":", 2)
			if len(parts) != 2 {
				continue
			}
			start, err0 := strconv.Atoi(
				strings.TrimSpace(parts[0]),
			)
			end, err1 := strconv.Atoi(
				strings.TrimSpace(parts[1]),
			)
			if err0 != nil || err1 != nil {
				continue
			}
			if end < start {
				ancli.Warnf(
					"invalid range (end < start): '%s'",
					tok,
				)
				continue
			}
			for j := start; j <= end; j++ {
				if j > max {
					ancli.Warnf(
						"selected index: '%v' is greater "+
							"than amount of items: '%v'",
						j,
						max,
					)
					continue
				}
				selectedNumbers = append(
					selectedNumbers, j,
				)
			}
			continue
		}
		v, err := strconv.Atoi(tok)
		if err == nil {
			if v > max {
				ancli.Warnf(
					"selected index: '%v' is greater "+
						"than amount of items: '%v'",
					v,
					max,
				)
				continue
			}
			selectedNumbers = append(
				selectedNumbers, v,
			)
		}
	}

	return selectedNumbers
}

func printSelectRow[T any](w io.Writer, i int, chats []T, formatRow func(int, T) (string, error)) error {
	item := chats[i]

	formatted, err := formatRow(i, item)
	if err != nil {
		return fmt.Errorf("failed to format row: %w", err)
	}

	fmt.Fprintln(w, formatted)
	return nil
}

func printSelectItemOptions[T any](page, pageSize, amItems int, items []T, choiesFormat string, formatRow func(int, T) (string, error)) (int, error) {
	pageIndex := page * pageSize
	listToIndex := pageIndex + pageSize
	if listToIndex > amItems {
		listToIndex = amItems
	}

	// Could this be "calculated" using "maths"
	// Yes. Most likely. Don't judge me.
	amPrinted := 0
	for i := pageIndex; i < listToIndex; i++ {
		amPrinted++
		printErr := printSelectRow(os.Stdout, i, items, formatRow)
		if printErr != nil {
			return 0, fmt.Errorf("failed to printRow: %w", printErr)
		}
	}
	fmt.Printf(choiesFormat, page, amItems/pageSize)

	return amPrinted, nil
}
