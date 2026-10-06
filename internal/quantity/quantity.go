// Package quantity parses whole numbers written with a scale, the way budgets
// are typed on a command line: "300k", "1.5M", "2Mi", "3e5". SI prefixes, IEC
// binary prefixes and scientific exponents are accepted, and the scale is
// applied with exact integer arithmetic, so "1.1k" is exactly 1100 and never
// 1100.0000000000002.
package quantity

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Quantity is a whole number parsed from a scaled spelling.
type Quantity int

// Int returns the number as a plain int.
func (q Quantity) Int() int { return int(q) }

var (
	// ErrSyntax reports a spelling that is not a number followed by one known
	// scale: a bad shape, an unknown suffix, or a suffix and an exponent at
	// the same time.
	ErrSyntax = errors.New("invalid quantity syntax")
	// ErrFraction reports a spelling whose value is not a whole number, such
	// as "1.2345k" (1234.5) or a bare "1.5".
	ErrFraction = errors.New("quantity is not a whole number")
	// ErrRange reports a value that does not fit in an int.
	ErrRange = errors.New("quantity out of range")
)

const suffixHint = `unknown suffix %q; use k, M, G, T or Ki, Mi, Gi, Ti, or an exponent like 3e5`

// maxScale bounds the power of ten the parser builds, so a hostile input
// cannot ask for a gigantic big.Int. Values past the bound miss an int by a
// wide margin: a large positive exponent is out of range, a large negative one
// is a fraction.
const maxScale = 10000

// decimalSuffixes maps the accepted SI prefixes to their power of ten. Case
// matters except for kilo: SI reads a bare m as milli, so "500m" must not
// silently become 500 million.
var decimalSuffixes = map[rune]int{'k': 3, 'K': 3, 'M': 6, 'G': 9, 'T': 12}

// binarySuffixBits maps the magnitude letter of an IEC prefix to its power of
// two. The trailing i is what makes a lower case magnitude letter safe, which
// is why "mi" is mebi while a bare "m" is rejected.
var binarySuffixBits = map[rune]uint{'k': 10, 'K': 10, 'm': 20, 'M': 20, 'g': 30, 'G': 30, 't': 40, 'T': 40}

// Parse reads a whole number with an optional scale. The accepted forms are a
// plain integer ("300000"), an SI prefix ("300k", "1.5M"), an IEC binary
// prefix ("2Mi") or a scientific exponent ("3e5"). A fractional mantissa is
// allowed when the result is a whole number; a suffix and an exponent cannot
// be combined.
func Parse(s string) (Quantity, error) {
	body, negative, err := trimSign(s)
	if err != nil {
		return 0, err
	}
	mantissaText, scaleText := splitMantissaEnd(body)
	intPart, fracPart, err := splitMantissa(mantissaText, s)
	if err != nil {
		return 0, err
	}
	mantissa, ok := new(big.Int).SetString(intPart+fracPart, 10)
	if !ok {
		return 0, fmt.Errorf("%w: %q has no number", ErrSyntax, s)
	}
	if negative {
		mantissa.Neg(mantissa)
	}
	return scale(mantissa, len(fracPart), scaleText, s)
}

// trimSign strips a leading sign from s and reports whether it was negative.
func trimSign(s string) (body string, negative bool, err error) {
	switch {
	case s == "":
		return "", false, fmt.Errorf("%w: empty value", ErrSyntax)
	case s[0] == '+':
		return s[1:], false, nil
	case s[0] == '-':
		return s[1:], true, nil
	}
	return s, false, nil
}

// splitMantissaEnd cuts the leading digits and dots from the body. The
// mantissa ends where the first character neither of those can be.
func splitMantissaEnd(body string) (mantissa, scale string) {
	i := 0
	for i < len(body) && (isDigit(body[i]) || body[i] == '.') {
		i++
	}
	return body[:i], body[i:]
}

// splitMantissa splits a verified digit-and-dot string into its integer and
// fraction digits.
func splitMantissa(mantissa, original string) (intPart, fracPart string, err error) {
	intPart, fracPart, hasDot := strings.Cut(mantissa, ".")
	switch {
	case intPart == "" && fracPart == "":
		return "", "", fmt.Errorf("%w: %q has no number", ErrSyntax, original)
	case hasDot && fracPart == "":
		return "", "", fmt.Errorf("%w: %q is missing digits after '.'", ErrSyntax, original)
	case strings.ContainsRune(fracPart, '.'):
		return "", "", fmt.Errorf("%w: %q has more than one '.'", ErrSyntax, original)
	}
	return intPart, fracPart, nil
}

// scale applies the suffix or exponent to the mantissa, then requires the
// result to be a whole number that fits an int.
func scale(mantissa *big.Int, fracDigits int, scaleText, original string) (Quantity, error) {
	multiplier := big.NewInt(1)
	exponent := 0
	switch {
	case scaleText == "":
	case isExponent(scaleText):
		digits := scaleText[1:]
		e, err := strconv.Atoi(digits)
		switch {
		case errors.Is(err, strconv.ErrRange) && strings.HasPrefix(digits, "-"):
			return 0, fmt.Errorf("%w: %q is smaller than one", ErrFraction, original)
		case errors.Is(err, strconv.ErrRange):
			return 0, fmt.Errorf("%w: %q is too large", ErrRange, original)
		case err != nil:
			return 0, fmt.Errorf("%w: %q has a bad exponent", ErrSyntax, original)
		}
		exponent = e
	default:
		m, e, err := suffixScale(scaleText)
		if err != nil {
			return 0, err
		}
		multiplier, exponent = m, e
	}

	exponent -= fracDigits
	switch {
	case exponent > maxScale:
		return 0, fmt.Errorf("%w: %q is too large", ErrRange, original)
	case exponent < -maxScale:
		return 0, fmt.Errorf("%w: %q is smaller than one", ErrFraction, original)
	}

	numerator := new(big.Int).Mul(mantissa, multiplier)
	denominator := big.NewInt(1)
	if exponent >= 0 {
		numerator.Mul(numerator, pow10(exponent))
	} else {
		denominator = pow10(-exponent)
	}
	value, remainder := new(big.Int).QuoRem(numerator, denominator, new(big.Int))
	if remainder.Sign() != 0 {
		return 0, fmt.Errorf("%w: %q is not a whole number", ErrFraction, original)
	}
	if !value.IsInt64() {
		return 0, fmt.Errorf("%w: %q does not fit in an int", ErrRange, original)
	}
	v := value.Int64()
	if int64(int(v)) != v {
		return 0, fmt.Errorf("%w: %q does not fit in an int", ErrRange, original)
	}
	return Quantity(v), nil
}

// isExponent reports whether the scale text is a scientific exponent rather
// than a suffix.
func isExponent(scaleText string) bool {
	return scaleText[0] == 'e' || scaleText[0] == 'E'
}

// suffixScale returns the multiplier and decimal exponent of a suffix, or an
// ErrSyntax naming the accepted set.
func suffixScale(suffix string) (*big.Int, int, error) {
	if len(suffix) == 1 {
		if power, ok := decimalSuffixes[rune(suffix[0])]; ok {
			return big.NewInt(1), power, nil
		}
	}
	if len(suffix) == 2 && (suffix[1] == 'i' || suffix[1] == 'I') {
		if bits, ok := binarySuffixBits[rune(suffix[0])]; ok {
			return new(big.Int).Lsh(big.NewInt(1), bits), 0, nil
		}
	}
	return nil, 0, fmt.Errorf("%w: "+suffixHint, ErrSyntax, suffix)
}

func pow10(exponent int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil)
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
