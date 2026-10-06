package quantity

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// TestParse_ScaledValues pins the accepted spellings and their exact values.
func TestParse_ScaledValues(t *testing.T) {
	testCases := []struct {
		desc string
		in   string
		want Quantity
	}{
		{"plain integer", "300000", 300000},
		{"zero", "0", 0},
		{"explicit plus", "+7", 7},
		{"negative", "-7", -7},

		{"kilo", "300k", 300000},
		{"kilo upper K", "300K", 300000},
		{"mega", "1M", 1_000_000},
		{"giga", "2G", 2_000_000_000},
		{"tera", "1T", 1_000_000_000_000},
		{"fractional mega", "1.5M", 1_500_000},
		{"fractional kilo", "0.5k", 500},
		{"fraction that lands on a whole number", "1.1k", 1100},
		{"leading dot", ".5k", 500},
		{"negative kilo", "-1k", -1000},

		{"kibi", "1Ki", 1024},
		{"kibi lower magnitude", "1ki", 1024},
		{"kibi mixed case", "1kI", 1024},
		{"mebi", "2Mi", 2 * 1024 * 1024},
		{"mebi lower magnitude", "2mi", 2 * 1024 * 1024},
		{"gibi", "1Gi", 1 << 30},
		{"tebi", "1Ti", 1 << 40},

		{"exponent", "3e5", 300000},
		{"exponent upper", "3E5", 300000},
		{"exponent with plus", "3e+5", 300000},
		{"negative exponent", "2500e-2", 25},
		{"fractional mantissa with exponent", "1.2E6", 1_200_000},
		{"zero exponent", "5e0", 5},
		{"zero times a large exponent", "0e9999", 0},
		{"negative mantissa with exponent", "-2e3", -2000},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			got, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("Parse(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestParse_Errors pins every rejection and its classification.
func TestParse_Errors(t *testing.T) {
	testCases := []struct {
		desc string
		in   string
		want error
	}{
		{"empty", "", ErrSyntax},
		{"sign only", "-", ErrSyntax},
		{"plus only", "+", ErrSyntax},
		{"letters", "abc", ErrSyntax},
		{"suffix without a number", "k", ErrSyntax},
		{"lone lower case milli", "500m", ErrSyntax},
		{"lone lower case giga", "5g", ErrSyntax},
		{"lone lower case tera", "5t", ErrSyntax},
		{"bare E is not a suffix", "5E", ErrSyntax},
		{"suffix and exponent together", "1e3k", ErrSyntax},
		{"exponent between suffixes", "1k5", ErrSyntax},
		{"two dots", "1.2.3", ErrSyntax},
		{"trailing dot", "1.", ErrSyntax},
		{"dot only", ".", ErrSyntax},
		{"empty exponent", "1e", ErrSyntax},
		{"fractional exponent", "1e1.5", ErrSyntax},
		{"space inside the value", "1 kb", ErrSyntax},
		{"underscore separator", "1_000", ErrSyntax},
		{"fraction without a suffix", "1.5", ErrFraction},
		{"fraction beyond the digits", "1.2345k", ErrFraction},
		{"exponent below one", "1e-1", ErrFraction},
		{"fraction far below one", "1e-99999", ErrFraction},
		{"beyond an int", "99999999999999999999", ErrRange},
		{"beyond an int through an exponent", "1e30", ErrRange},
		{"absurd exponent", "1e99999", ErrRange},
		{"exponent beyond int64", "1e99999999999999999999", ErrRange},
		{"negative exponent beyond int64", "1e-99999999999999999999", ErrFraction},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			got, err := Parse(tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Parse(%q) error = %v, want %v", tc.in, err, tc.want)
			}
			if got != 0 {
				t.Fatalf("Parse(%q) = %d on error, want 0", tc.in, got)
			}
		})
	}
}

// TestParse_SyntaxErrorTeachesTheNotation pins that a rejected spelling names
// the accepted set, so the error is self-documenting.
func TestParse_SyntaxErrorTeachesTheNotation(t *testing.T) {
	_, err := Parse("500m")
	if !errors.Is(err, ErrSyntax) {
		t.Fatalf("error = %v, want ErrSyntax", err)
	}
	for _, want := range []string{`"m"`, "k, M, G, T", "Ki, Mi, Gi, Ti", "3e5"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must mention %q", err, want)
		}
	}
}

// TestQuantity_Int pins the conversion to a plain int.
func TestQuantity_Int(t *testing.T) {
	q, err := Parse("300k")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.Int() != 300000 {
		t.Fatalf("Int() = %d, want 300000", q.Int())
	}
}

// TestParse_RangeFollowsIntWidth pins that the value must fit the platform
// int, not merely an int64.
func TestParse_RangeFollowsIntWidth(t *testing.T) {
	got, err := Parse("3G")
	if strconv.IntSize == 32 {
		if !errors.Is(err, ErrRange) {
			t.Fatalf("Parse(3G) error = %v on a 32 bit int, want ErrRange", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("Parse(3G): %v", err)
	}
	if got.Int() != 3_000_000_000 {
		t.Fatalf("Parse(3G) = %d, want 3000000000", got)
	}
}
