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
	withBack bool,
) ([]int, error) {
	fmt.Println(Colorize(ThemePrimaryColor(), header))
	headerWidth := utf8.RuneCount([]byte(header))
	line := strings.Repeat("-", headerWidth)
	fmt.Printf("%v\n", Colorize(ThemePrimaryColor(), line))

	page := 0
	amItems := len(items)
	lastPage := pageCount(amItems, pageSize)
	noNumberSelected := true
	selectedNumbers := []int{}
	amPrinted := 0
	for noNumberSelected {
		tmpAmPrinted, printErr := printSelectItemOptions(
			page,
			pageSize,
			amItems,
			items,
			choicesFormatForSelection(choicesFormat, withBack),
			rowFormater,
		)
		if printErr != nil {
			return []int{}, fmt.Errorf("failed to print options: %w", printErr)
		}
		amPrinted = tmpAmPrinted
		choice, usrReadErr := ReadUserInput()
		if usrReadErr != nil {
			return []int{}, fmt.Errorf("failed to read table selection: %w", usrReadErr)
		}
		goPrevPageChoices := []string{"p", "prev"}
		goBackChoices := []string{"b", "back"}
		toClear := amPrinted + 1
		if slices.Contains(goPrevPageChoices, choice) {
			page--
			if page < 0 {
				page = lastPage
			}
		} else if withBack && slices.Contains(goBackChoices, choice) {
			return []int{}, fmt.Errorf("user chose to go back: %w", ErrBack)
		} else {
			selectedNumbers = parseNumbersFromString(choice, amItems)
			noNumberSelected = len(selectedNumbers) == 0
			if !noNumberSelected {
				toClear += 2
				break
			}
			page++
			if page > lastPage {
				page = 0
			}
		}
		err := ClearTermTo(os.Stdout, -1, toClear)
		if err != nil {
			return []int{}, fmt.Errorf("failed to clear term: %w", err)
		}
	}

	if onlyOneSelect && len(selectedNumbers) > 1 {
		return []int{}, fmt.Errorf("only one selected number supported. selected indices: %v", selectedNumbers)
	}

	return selectedNumbers, nil
}

func choicesFormatForSelection(choicesFormat string, withBack bool) string {
	if !withBack {
		return choicesFormat
	}
	trimmed := strings.TrimSuffix(choicesFormat, "): ")
	return fmt.Sprintf("%s, [b]ack): ", trimmed)
}

func pageCount(amItems, pageSize int) int {
	if pageSize <= 0 || amItems <= 0 {
		return 0
	}
	return (amItems - 1) / pageSize
}

func parseNumbersFromString(choice string, max int) []int {
	selectedNumbers := make([]int, 0)
	tokens := strings.SplitSeq(choice, ",")
	for tok := range tokens {
		tok = strings.TrimSpace(tok)
		if strings.Contains(tok, ":") {
			parts := strings.SplitN(tok, ":", 2)
			if len(parts) != 2 {
				continue
			}
			start, err0 := strconv.Atoi(strings.TrimSpace(parts[0]))
			end, err1 := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err0 != nil || err1 != nil {
				continue
			}
			if end < start {
				ancli.Warnf("invalid range (end < start): '%s'", tok)
				continue
			}
			for j := start; j <= end; j++ {
				if j > max {
					ancli.Warnf("selected index: '%v' is greater than amount of items: '%v'", j, max)
					continue
				}
				selectedNumbers = append(selectedNumbers, j)
			}
			continue
		}
		v, err := strconv.Atoi(tok)
		if err == nil {
			if v > max {
				ancli.Warnf("selected index: '%v' is greater than amount of items: '%v'", v, max)
				continue
			}
			selectedNumbers = append(selectedNumbers, v)
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

	fmt.Fprintln(w, Colorize(ThemeBreadtextColor(), formatted))
	return nil
}

func printSelectItemOptions[T any](page, pageSize, amItems int, items []T, choiesFormat string, formatRow func(int, T) (string, error)) (int, error) {
	pageIndex := page * pageSize
	listToIndex := min(pageIndex+pageSize, amItems)

	amPrinted := 0
	for i := pageIndex; i < listToIndex; i++ {
		amPrinted++
		printErr := printSelectRow(os.Stdout, i, items, formatRow)
		if printErr != nil {
			return 0, fmt.Errorf("failed to print row: %w", printErr)
		}
	}
	if amItems <= pageSize {
		fmt.Print(Colorize(ThemeSecondaryColor(), choiesFormat))
	} else {
		fmt.Print(Colorize(ThemeSecondaryColor(), fmt.Sprintf("(page: (%v/%v). %v", page, pageCount(amItems, pageSize), choiesFormat)))
	}

	return amPrinted, nil
}
