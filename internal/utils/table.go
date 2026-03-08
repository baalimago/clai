package utils

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type CustomTableAction struct {
	// Format for the the menu item to be printed. Include short and long Format. Example [b]ack
	Format string
	// Short name back -> "b"
	Short string
	// Long name "back"
	Long   string
	Action func() error
}

// SelectFromTable by:
// 1. Listing rows according to rowFormater
// 2. Returning a list of chosen numbers
//
// Valid inputs:
//   - nr = int < len(items)
//   - nr,nr,nr - This selects multiple numbers
//   - nr:nr,nr,nr:nr - This selects two ranges of nr, as well as a singular nr
func SelectFromTable[T any](header string, items []T,
	selectionType string,
	rowFormater func(int, T) (string, error),
	pageSize int,
	onlyOneSelect bool,
	additionalTableActions []CustomTableAction,
) ([]int, error) {
	fmt.Println(Colorize(ThemePrimaryColor(), header))
	headerWidth := visibleRuneCount(header)
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
			maybeAddAdditionalActions(selectionType, additionalTableActions),
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

		for _, ata := range additionalTableActions {
			tmpChoices := []string{ata.Long, ata.Short}
			if slices.Contains(tmpChoices, choice) {
				if ata.Action == nil {
					return []int{}, fmt.Errorf("action %q lacks action", ata.Long)
				}
				return []int{}, ata.Action()
			}
		}

		goPrevPageChoices := []string{"p", "prev"}
		toClear := amPrinted + 1
		if slices.Contains(goPrevPageChoices, choice) {
			page--
			if page < 0 {
				page = lastPage
			}
		} else {
			selectedNumbers = parseNumbersFromString(choice, amItems-1)
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

func maybeAddAdditionalActions(choicesFormat string, additionalTableActions []CustomTableAction) string {
	if len(additionalTableActions) == 0 {
		return choicesFormat
	}

	trimmed := strings.TrimSpace(choicesFormat)
	trimmed = strings.TrimSuffix(trimmed, ":")
	trimmed = strings.TrimSpace(trimmed)

	sb := strings.Builder{}
	sb.WriteString(trimmed)
	for _, ata := range additionalTableActions {
		if sb.Len() > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(ata.Format)
	}
	sb.WriteString(": ")

	return sb.String()
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
				ancli.Warnf("invalid range (end < start): %q", tok)
				continue
			}
			for j := start; j <= end; j++ {
				if j > max {
					ancli.Warnf("selected index %q is greater than amount of items %q", strconv.Itoa(j), strconv.Itoa(max))
					continue
				}
				selectedNumbers = append(selectedNumbers, j)
			}
			continue
		}
		v, err := strconv.Atoi(tok)
		if err == nil {
			if v > max {
				ancli.Warnf("selected index %q is greater than amount of items %q", tok, strconv.Itoa(max))
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

func formatChoicesPrompt(choicesFormat string, page, lastPage int) string {
	if strings.Contains(choicesFormat, "%") {
		return fmt.Sprintf(choicesFormat, page, lastPage)
	}
	return choicesFormat
}

func sanitizePagedPrompt(prompt string) string {
	sanitized := strings.TrimSpace(prompt)
	sanitized = strings.TrimSuffix(sanitized, ":")
	sanitized = strings.TrimSpace(sanitized)

	redundantSuffixes := []string{
		", [p]rev, [q]uit",
		"[p]rev, [q]uit",
	}
	for _, suffix := range redundantSuffixes {
		sanitized = strings.TrimSuffix(sanitized, suffix)
		sanitized = strings.TrimSpace(sanitized)
	}
	return sanitized
}

func printSelectItemOptions[T any](page, pageSize, amItems int, items []T, choicesFormat string, formatRow func(int, T) (string, error)) (int, error) {
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

	lastPage := pageCount(amItems, pageSize)
	if amItems <= pageSize {
		fmt.Print(Colorize(ThemeSecondaryColor(), formatChoicesPrompt(choicesFormat, 0, 0)))
	} else {
		innerPrompt := sanitizePagedPrompt(formatChoicesPrompt(choicesFormat, page, lastPage))
		fmt.Print(Colorize(
			ThemeSecondaryColor(),
			fmt.Sprintf(
				"(page: (%v/%v). %s, next: [<enter>]/[n]ext, [p]rev, [q]uit): ",
				page,
				lastPage,
				innerPrompt,
			),
		))
	}

	return amPrinted, nil
}
