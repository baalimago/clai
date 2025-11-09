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
// 1. Listing rows according to rowFormater (note that header isn't listed)
// 2. Returning a list of chosen numbers
// 3. Amount of printed rows on last iteration (this is to clear terminal, if needed)
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
			maybeInts := strings.Split(choice, ",")
			for _, maybeIntAsString := range maybeInts {
				maybeIntAsInt, intIfNil := strconv.Atoi(maybeIntAsString)
				if intIfNil == nil {
					if maybeIntAsInt > amItems {
						ancli.Warnf("selected index: '%v' is greater than amount of items: '%v'", maybeIntAsInt, amItems)
						continue
					}
					selectedNumbers = append(selectedNumbers, maybeIntAsInt)
				}
			}

			noNumberSelected = len(selectedNumbers) == 0
			if !noNumberSelected {
				// +2 in case of break since we want to remove "---" line and header
				toClear += 2
			}
			page++
			if page > amItems/pageSize {
				page = 0
			}
		}
		err := ClearTermTo(-1, toClear)
		if err != nil {
			return []int{}, fmt.Errorf("failed to clear term: %w", err)
		}

	}

	if onlyOneSelect && len(selectedNumbers) > 1 {
		return []int{}, fmt.Errorf("only one selected number supported. Selected indices: '%v'", selectedNumbers)
	}

	return selectedNumbers, nil
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
