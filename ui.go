package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	fuzzyfinder "github.com/ktr0731/go-fuzzyfinder"
	"golang.org/x/term"
)

var errCancelled = errors.New("cancelled")

var (
	stdoutTTY = term.IsTerminal(int(os.Stdout.Fd()))
	stderrTTY = term.IsTerminal(int(os.Stderr.Fd()))
	useColor  = stdoutTTY && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
)

func paint(code, s string) string {
	if !useColor || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func bold(s string) string    { return paint("1", s) }
func dim(s string) string     { return paint("2", s) }
func red(s string) string     { return paint("31", s) }
func green(s string) string   { return paint("32", s) }
func yellow(s string) string  { return paint("33", s) }
func blue(s string) string    { return paint("34", s) }
func magenta(s string) string { return paint("35", s) }
func cyan(s string) string    { return paint("36", s) }

// pick shows a fuzzy finder and returns the chosen indexes. Esc/Ctrl-C
// returns errCancelled. In multi mode Tab toggles items.
func pick(n int, label func(int) string, multi bool, query, header string, preview func(int) string) ([]int, error) {
	if n == 0 {
		return nil, fmt.Errorf("nothing to pick from")
	}
	opts := []fuzzyfinder.Option{fuzzyfinder.WithPromptString("❯ ")}
	if query != "" {
		opts = append(opts, fuzzyfinder.WithQuery(query))
	}
	if header != "" {
		opts = append(opts, fuzzyfinder.WithHeader(header))
	}
	if preview != nil {
		opts = append(opts, fuzzyfinder.WithPreviewWindow(func(i, _, _ int) string {
			if i < 0 {
				return ""
			}
			return preview(i)
		}))
	}
	items := make([]struct{}, n)
	var idx []int
	var err error
	if multi {
		idx, err = fuzzyfinder.FindMulti(items, label, opts...)
	} else {
		var i int
		i, err = fuzzyfinder.Find(items, label, opts...)
		idx = []int{i}
	}
	if errors.Is(err, fuzzyfinder.ErrAbort) {
		return nil, errCancelled
	}
	return idx, err
}

// spinner draws a progress indicator on stderr until the returned func is called.
func spinner(msg string) func() {
	if !stderrTTY {
		return func() {}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			fmt.Fprintf(os.Stderr, "\r%c %s", frames[i%len(frames)], msg)
			select {
			case <-done:
				fmt.Fprint(os.Stderr, "\r\x1b[K")
				return
			case <-t.C:
			}
		}
	}()
	return func() { close(done); <-finished }
}

func confirm(q string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", q)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute") + " ago"
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	case d < 30*24*time.Hour:
		return plural(int(d.Hours()/24), "day") + " ago"
	case d < 365*24*time.Hour:
		return plural(int(d.Hours()/24/30), "month") + " ago"
	}
	return plural(int(d.Hours()/24/365), "year") + " ago"
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// setCD asks the shell wrapper (see "gx shell") to change directory. Without
// the wrapper the path is printed on stdout so `cd "$(gx j)"` still works.
func setCD(dir string) error {
	if f := os.Getenv("GX_CD_FILE"); f != "" {
		return os.WriteFile(f, []byte(dir), 0o600)
	}
	fmt.Println(dir)
	if stdoutTTY {
		fmt.Fprintln(os.Stderr, dim("tip: add `eval \"$(gx shell bash)\"` to your shell rc so gx can cd for you"))
	}
	return nil
}
