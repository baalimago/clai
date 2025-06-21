package utils

import (
	"errors"
	"math/rand"
)

func RandomPrefix() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, 10)
	for i := range result {
		result[i] = charset[rand.Intn(len(charset))]
	}

	return string(result)
}

// GetFirstTokens returns the first n tokens of the prompt, or the whole prompt if it has less than n tokens
func GetFirstTokens(prompt []string, n int) []string {
	ret := make([]string, 0)
	for _, token := range prompt {
		if token == "" {
			continue
		}
		if len(ret) < n {
			ret = append(ret, token)
		} else {
			return ret
		}
	}
	return ret
}

// DeleteRange removes elements in [start, end] (inclusive) from the slice and returns the new slice.
// Panics if indices are invalid. Works for any slice type (Go 1.18+).
func DeleteRange[T any](s []T, start, end int) ([]T, error) {
	if start < 0 || end >= len(s) || start > end {
		return s, errors.New("invalid range for DeleteRange")
	}
	return append(s[:start], s[end+1:]...), nil
}
