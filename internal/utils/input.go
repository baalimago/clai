package utils

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
)

// ReadUserInput and return on interrupt channel
func ReadUserInput() (string, error) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	defer signal.Stop(sigChan)
	inputChan := make(chan string)
	errChan := make(chan error)

	go func() {
		reader := bufio.NewReader(os.Stdin)
		userInput, err := reader.ReadString('\n')
		if err != nil {
			errChan <- err
			return
		}
		inputChan <- userInput
	}()

	select {
	case <-sigChan:
		return "", ErrUserInitiatedExit
	case err := <-errChan:
		return "", fmt.Errorf("failed to read user input: %w", err)
	case userInput, open := <-inputChan:
		if open {
			trimmedInput := strings.TrimSpace(userInput)
			quitters := []string{"q", "quit", "e", "exit"}
			if slices.Contains(quitters, trimmedInput) {
				return "", ErrUserInitiatedExit
			}
			return trimmedInput, nil
		} else {
			return "", errors.New("user input channel closed. Not sure how we ended up here🤔")
		}
	}
}
