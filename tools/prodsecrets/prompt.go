package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// TerminalPrompter asks on the controlling terminal with echo switched off.
// The label goes to stderr so stdout stays a clean report.
type TerminalPrompter struct {
	In  *os.File
	Out io.Writer
}

// Ask prints the label and reads one line without echo. An empty line means
// "leave it for now"; the caller decides whether that is allowed.
func (t TerminalPrompter) Ask(label string) (string, error) {
	in := t.In
	if in == nil {
		in = os.Stdin
	}
	out := t.Out
	if out == nil {
		out = os.Stderr
	}
	fmt.Fprintf(out, "%s\n  (typed without echo; Enter alone leaves it empty): ", label)
	v, err := readNoEcho(in)
	fmt.Fprintln(out)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(v, "\r\n"), nil
}

func readLine(f *os.File) (string, error) {
	r := bufio.NewReader(f)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return line, nil
}

// MapPrompter answers from a fixed table (tests and --answers files).
type MapPrompter struct {
	Answers map[string]string
	Asked   []string
}

// Ask returns the answer for the label, "" when none.
func (m *MapPrompter) Ask(label string) (string, error) {
	m.Asked = append(m.Asked, label)
	return m.Answers[label], nil
}
