package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ANSI color codes (truecolor not needed; basic 8/16 colors are universal).
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorMagenta= "\033[35m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
)

// detect whether stdout/stderr is a TTY (so we can disable colors when piped).
var isTTY = isTerminal()

func isTerminal() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// colorize wraps s in color codes only if stdout is a TTY.
func colorize(color, s string) string {
	if !isTTY {
		return s
	}
	return color + s + colorReset
}

// Styling shortcuts.
func bold(s string) string   { return colorize(colorBold, s) }
func dim(s string) string    { return colorize(colorDim, s) }
func red(s string) string    { return colorize(colorRed, s) }
func green(s string) string  { return colorize(colorGreen, s) }
func yellow(s string) string { return colorize(colorYellow, s) }
func blue(s string) string   { return colorize(colorBlue, s) }
func cyan(s string) string   { return colorize(colorCyan, s) }
func magenta(s string) string { return colorize(colorMagenta, s) }
func gray(s string) string   { return colorize(colorGray, s) }

// Icons (Unicode, work in most terminals; fall back to ASCII if not TTY).
var (
	iconCheck = "✓"
	iconCross = "✗"
	iconWarn  = "!"
	iconArrow = "→"
	iconInfo  = "•"
	iconRocket= "🚀"
)

func init() {
	if !isTTY {
		iconCheck = "OK"
		iconCross = "X"
		iconWarn  = "!"
		iconArrow = "->"
		iconInfo  = "*"
		iconRocket= ""
	}
}

// printHeader prints a banner / title.
func printHeader(title string) {
	line := strings.Repeat("─", 60)
	fmt.Fprintf(os.Stderr, "\n%s%s%s\n", colorBold+colorCyan, title, colorReset)
	fmt.Fprintf(os.Stderr, "%s%s%s\n", colorGray, line, colorReset)
}

// printStep prints a numbered phase header.
func printStep(n, total int, title string) {
	badge := fmt.Sprintf("%s[%d/%d]%s", colorBold+colorBlue, n, total, colorReset)
	fmt.Fprintf(os.Stderr, "\n%s %s%s%s\n", badge, colorBold, title, colorReset)
}

// printOK prints a green ✓ success line.
func printOK(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s%s%s %s\n",
		colorGreen, iconCheck, colorReset, fmt.Sprintf(format, args...))
}

// printFail prints a red ✗ error line.
func printFail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s%s%s %s\n",
		colorRed, iconCross, colorReset, fmt.Sprintf(format, args...))
}

// printWarn prints a yellow ! warning line.
func printWarn(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s%s%s %s\n",
		colorYellow, iconWarn, colorReset, fmt.Sprintf(format, args...))
}

// printInfo prints a dim info bullet.
func printInfo(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "  %s%s%s %s\n",
		colorGray, iconInfo, colorReset, fmt.Sprintf(format, args...))
}

// printKeyValue prints a key/value pair aligned.
func printKeyValue(key, value string) {
	fmt.Fprintf(os.Stderr, "  %s%-20s%s %s\n",
		colorDim, key, colorReset, bold(value))
}

// progress is an inline spinner that runs in a goroutine while a long
// operation is in progress. Use Start() to begin and Stop() to end.
type spinner struct {
	mu      sync.Mutex
	frames  []string
	msg     string
	active  bool
	stopCh  chan struct{}
	doneCh  chan struct{}
	started time.Time
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// newSpinner creates a new spinner with the given message.
func newSpinner(msg string) *spinner {
	return &spinner{
		frames: spinnerFrames,
		msg:    msg,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start launches the spinner in a background goroutine.
func (s *spinner) Start() {
	if !isTTY {
		// No TTY: just print the message once.
		fmt.Fprintf(os.Stderr, "  %s%s%s\n", colorDim, s.msg, colorReset)
		return
	}
	s.active = true
	s.started = time.Now()
	go s.run()
}

func (s *spinner) run() {
	defer close(s.doneCh)
	i := 0
	for {
		select {
		case <-s.stopCh:
			// Clear the line (carriage return + spaces).
			fmt.Fprintf(os.Stderr, "\r\033[K")
			return
		default:
			s.mu.Lock()
			frame := s.frames[i%len(s.frames)]
			msg := s.msg
			s.mu.Unlock()
			elapsed := time.Since(s.started).Truncate(time.Second)
			fmt.Fprintf(os.Stderr, "\r%s%s%s %s %s%s%s",
				colorCyan, frame, colorReset,
				msg,
				colorGray, elapsed, colorReset)
			time.Sleep(80 * time.Millisecond)
			i++
		}
	}
}

// Update changes the spinner message on the fly.
func (s *spinner) Update(msg string) {
	s.mu.Lock()
	s.msg = msg
	s.mu.Unlock()
}

// Stop ends the spinner and replaces it with a final status line.
func (s *spinner) Stop(success bool, finalMsg string) {
	if !isTTY || !s.active {
		if success {
			printOK("%s", finalMsg)
		} else {
			printFail("%s", finalMsg)
		}
		return
	}
	close(s.stopCh)
	<-s.doneCh
	if success {
		fmt.Fprintf(os.Stderr, "%s%s%s %s\n",
			colorGreen, iconCheck, colorReset, finalMsg)
	} else {
		fmt.Fprintf(os.Stderr, "%s%s%s %s\n",
			colorRed, iconCross, colorReset, finalMsg)
	}
}

// progressBar renders a simple inline progress percentage based on bytes
// processed vs total. Useful for rsync / mysqldump output.
//
// Usage:
//   pb := newProgressBar("copying files", totalBytes)
//   pb.Start()
//   // ... update pb.current as data flows ...
//   pb.Done()
type progressBar struct {
	label   string
	total   int64
	current int64
	mu      sync.Mutex
	active  bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// newProgressBar creates a new progress bar.
func newProgressBar(label string, total int64) *progressBar {
	return &progressBar{
		label:  label,
		total:  total,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start launches the progress bar in a background goroutine.
func (p *progressBar) Start() {
	if !isTTY {
		fmt.Fprintf(os.Stderr, "  %s%s%s\n", colorDim, p.label, colorReset)
		return
	}
	p.active = true
	go p.run()
}

func (p *progressBar) run() {
	defer close(p.doneCh)
	for {
		select {
		case <-p.stopCh:
			fmt.Fprintf(os.Stderr, "\r\033[K")
			return
		default:
			p.mu.Lock()
			cur := p.current
			tot := p.total
			p.mu.Unlock()
			p.render(cur, tot)
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func (p *progressBar) render(cur, tot int64) {
	pct := 0.0
	if tot > 0 {
		pct = float64(cur) / float64(tot) * 100.0
		if pct > 100 {
			pct = 100
		}
	}
	// Bar width: 30 chars
	width := 30
	filled := int(pct / 100.0 * float64(width))
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	fmt.Fprintf(os.Stderr, "\r  %s%s%s [%s] %s%5.1f%%%s %s%s%s",
		colorCyan, p.label, colorReset,
		colorGreen+bar+colorReset,
		colorBold, pct, colorReset,
		colorGray, humanSize(cur), colorReset)
}

// Update sets the current value (thread-safe).
func (p *progressBar) Update(n int64) {
	p.mu.Lock()
	p.current = n
	p.mu.Unlock()
}

// Done stops the progress bar.
func (p *progressBar) Done() {
	if !isTTY || !p.active {
		return
	}
	close(p.stopCh)
	<-p.doneCh
}

// printBanner prints the startup banner with version.
func printBanner(ver, commitStr, domain string) {
	if isTTY {
		fmt.Fprintf(os.Stderr, "\n%s%s magento-staging%s %s%s%s %s(%s)%s\n",
			colorBold+colorMagenta, iconRocket, colorReset,
			colorBold+colorCyan, ver, colorReset,
			colorGray, commitStr, colorReset)
	} else {
		fmt.Fprintf(os.Stderr, "\nmagento-staging %s (%s)\n", ver, commitStr)
	}
	fmt.Fprintf(os.Stderr, "%s Creating staging for %s%s%s\n\n",
		colorGray, colorBold+colorGreen, domain, colorReset)
}

// printFinalBanner prints a colored success/failure banner.
func printFinalBanner(success bool) {
	line := strings.Repeat("═", 60)
	color := colorGreen
	icon := iconCheck
	msg := " Staging created successfully"
	if !success {
		color = colorRed
		icon = iconCross
		msg = " Staging creation FAILED"
	}
	if isTTY {
		fmt.Fprintf(os.Stderr, "\n%s%s%s\n", color, line, colorReset)
		fmt.Fprintf(os.Stderr, "%s%s%s%s%s\n", color, icon, msg, strings.Repeat(" ", 60-len(msg)-len(icon)), colorReset)
		fmt.Fprintf(os.Stderr, "%s%s%s\n", color, line, colorReset)
	} else {
		fmt.Fprintf(os.Stderr, "\n%s\n%s%s\n%s\n", line, icon, msg, line)
	}
}
