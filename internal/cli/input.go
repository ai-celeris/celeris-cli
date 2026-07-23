package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// stdinIsPiped reports whether stdin carries piped/redirected data.
func stdinIsPiped() bool {
	return !term.IsTerminal(int(os.Stdin.Fd()))
}

// stdoutIsTTY reports whether stdout is an interactive terminal.
func stdoutIsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// stdinHintDelay is how long stdin may block before the user is told why the
// process appears to be doing nothing.
const stdinHintDelay = 2 * time.Second

func readAllStdin() (string, error) {
	// Reading from a pipe that nobody has closed yet blocks indefinitely and
	// looks exactly like a hang. Say what is happening, once, on stderr.
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTimer(stdinHintDelay)
		defer t.Stop()
		select {
		case <-done:
		case <-t.C:
			fmt.Fprintln(os.Stderr, "celeris: reading prompt from stdin; press Ctrl-D to finish or Ctrl-C to cancel")
		}
	}()
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// resolveInput expands a flag value using the openai-cli conventions:
// "@path" reads a file, "-" reads stdin, "\@..." escapes a literal @, and
// anything else is used verbatim. An empty value falls back to piped stdin
// when available.
func resolveInput(value string) (string, bool, error) {
	switch {
	case value == "-":
		s, err := readAllStdin()
		return s, err == nil, err
	case strings.HasPrefix(value, "\\@"):
		return value[1:], true, nil
	case strings.HasPrefix(value, "@"):
		data, err := os.ReadFile(value[1:])
		if err != nil {
			return "", false, err
		}
		return string(data), true, nil
	case value != "":
		return value, true, nil
	case stdinIsPiped():
		s, err := readAllStdin()
		if err != nil {
			return "", false, err
		}
		if strings.TrimSpace(s) == "" {
			return "", false, nil
		}
		return s, true, nil
	default:
		return "", false, nil
	}
}
