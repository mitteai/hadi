// Package ui is hadi's voice. One line per fact, with the box, the step, and
// the duration. No spinners, no ANSI tricks: a TTY and a CI log see the same
// text. Verbosity is reserved for the moments money is in motion.
package ui

import (
	"fmt"
	"os"
	"time"
)

// Step prints a lifecycle line: [host] label   detail   [ok] dur.
func Step(host, label, detail string, dur time.Duration, ok bool) {
	status := ""
	if !ok {
		status = "  FAILED"
	}
	fmt.Printf("[%s] %-8s %-45s%s  %s\n", host, label, detail, status, fmtDur(dur))
}

// Say prints a plain narrative line.
func Say(format string, a ...any) { fmt.Printf(format+"\n", a...) }

// Detail prints indented evidence (journal tails, health bodies).
func Detail(lines string) {
	for _, l := range splitLines(lines) {
		fmt.Printf("    %s\n", l)
	}
}

// Fail prints the message and exits 1: deploy failed, service is safe.
func Fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

// Usage prints the message and exits 2: config or usage error, nothing touched.
func Usage(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(2)
}

func fmtDur(d time.Duration) string {
	if d == 0 {
		return ""
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
