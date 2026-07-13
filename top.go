package main

// hadi top: an htop-like dashboard. Three views, one shared log pane:
//
//   fleet view    services on the left   · logs from every service
//   service view  its boxes on the left  · logs from that service's boxes
//   box view      vitals on the left     · logs from that box
//
// enter drills in, esc goes back, / filters the logs live. Hand-rolled ANSI
// on purpose: raw mode + escape codes are boring and sufficient; a TUI
// framework would be hadi's biggest dependency for its least critical
// feature.

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"

	"github.com/mitteai/hadi/internal/config"
	"github.com/mitteai/hadi/internal/discover"
	"github.com/mitteai/hadi/internal/sshx"
	"github.com/mitteai/hadi/internal/ui"
)

const (
	viewFleet = iota
	viewService
	viewBox

	logCap = 4000
	leftW  = 34
)

type logLine struct {
	svc, box, text string
}

type boxInfo struct {
	addr   string
	live   int
	sha    string
	health string
}

type svcInfo struct {
	name   string
	boxes  []boxInfo
	colors []int
}

type top struct {
	mu   sync.Mutex
	zone string
	key  ssh.Signer

	services []svcInfo
	logs     []logLine
	vitals   []string // box view left pane
	status   string

	view               int
	selSvc, selBox     int
	filter             string
	typing             bool
	draft              string
	logOff             int // scroll offset from the tail

	streams map[string]bool // box addr → stream running
	quit    chan struct{}
}

func cmdTop(service, zoneFlag, sshKeyFlag string) {
	zone := zoneFor(zoneFlag)
	if zone == "" {
		ui.Usage("hadi top needs a zone: --zone, a local deploy.json, or HADI_ZONE")
	}
	key, err := sshx.LoadKey(sshKeyFlag)
	if err != nil {
		ui.Usage("%v", err)
	}

	t := &top{zone: zone, key: key, streams: map[string]bool{}, quit: make(chan struct{})}
	if err := t.refresh(); err != nil {
		ui.Fail("%v", err)
	}
	if service != "" {
		for i, s := range t.services {
			if s.name == service {
				t.view, t.selSvc = viewService, i
			}
		}
	}

	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		ui.Fail("hadi top needs a terminal: %v", err)
	}
	fmt.Print("\x1b[?1049h\x1b[?25l") // alt screen, hide cursor
	defer func() {
		fmt.Print("\x1b[?1049l\x1b[?25h")
		_ = term.Restore(int(os.Stdin.Fd()), old)
	}()

	keys := make(chan []byte, 8)
	go func() {
		buf := make([]byte, 64)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			b := make([]byte, n)
			copy(b, buf[:n])
			keys <- b
		}
	}()

	go t.refreshLoop()
	go t.vitalsLoop()

	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	t.render()
	for {
		select {
		case <-tick.C:
			t.render()
		case b := <-keys:
			if t.handle(b) {
				close(t.quit)
				return
			}
			t.render()
		}
	}
}

// handle processes a chunk of input byte-by-byte (terminals batch on paste,
// and scripted input arrives batched too); returns true to quit.
func (t *top) handle(b []byte) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	for i := 0; i < len(b); i++ {
		c := b[i]

		if t.typing {
			switch {
			case c == 3: // ctrl-c
				return true
			case c == 0x1b:
				t.typing, t.draft = false, ""
			case c == '\r':
				t.filter, t.typing = t.draft, false
			case c == 0x7f || c == 0x08:
				if len(t.draft) > 0 {
					t.draft = t.draft[:len(t.draft)-1]
				}
			case c >= 0x20 && c < 0x7f:
				t.draft += string(c)
			}
			continue
		}

		switch {
		case c == 3: // ctrl-c
			return true
		case c == 'q':
			if t.view == viewFleet {
				return true
			}
			t.back()
		case c == 0x1b && i+2 < len(b) && b[i+1] == '[':
			switch b[i+2] {
			case 'A':
				t.moveSel(-1)
			case 'B':
				t.moveSel(1)
			case 'C':
				t.drill()
			case 'D':
				t.back()
			}
			i += 2
		case c == 0x1b:
			t.back()
		case c == 'k':
			t.moveSel(-1)
		case c == 'j':
			t.moveSel(1)
		case c == '\r':
			t.drill()
		case c == '/':
			t.typing, t.draft = true, t.filter
		case c == 'c':
			t.filter = ""
		case c == 'g':
			t.logOff = 0
		case c == 'u':
			t.logOff += 10
		case c == 'd':
			if t.logOff >= 10 {
				t.logOff -= 10
			} else {
				t.logOff = 0
			}
		}
	}
	return false
}

func (t *top) moveSel(d int) {
	switch t.view {
	case viewFleet:
		t.selSvc = clamp(t.selSvc+d, len(t.services))
	case viewService:
		if t.selSvc < len(t.services) {
			t.selBox = clamp(t.selBox+d, len(t.services[t.selSvc].boxes))
		}
	}
}

func (t *top) drill() {
	switch t.view {
	case viewFleet:
		if len(t.services) > 0 {
			t.view, t.selBox = viewService, 0
		}
	case viewService:
		if t.selSvc < len(t.services) && len(t.services[t.selSvc].boxes) > 0 {
			t.view = viewBox
			t.vitals = []string{"loading..."}
		}
	}
}

func (t *top) back() {
	switch t.view {
	case viewBox:
		t.view = viewService
	case viewService:
		t.view = viewFleet
	}
}

func clamp(v, n int) int {
	if v < 0 {
		return 0
	}
	if v >= n && n > 0 {
		return n - 1
	}
	if n == 0 {
		return 0
	}
	return v
}

// refresh fetches the fleet: services, boxes, per-box state. Also starts a
// log stream for any box it hasn't seen yet.
func (t *top) refresh() error {
	names, err := discover.Services(t.zone)
	if err != nil {
		return err
	}
	var services []svcInfo
	for _, name := range names {
		boxes, err := discover.Boxes(name, t.zone, nil)
		if err != nil {
			continue
		}
		svc := svcInfo{name: name}
		for _, addr := range boxes {
			bi := boxInfo{addr: addr, health: "?"}
			cl, err := sshx.Dial(addr, t.key)
			if err == nil {
				if st, _ := readState(cl, name); st != nil && st.Config != nil {
					st.Config.ApplyDefaults()
					svc.colors = st.Config.Colors
					bi.live, _ = activeColor(cl, st.Config)
					bi.sha = st.SHA
					bi.health = "ok"
					if _, err := cl.Run(healthCmd(st.Config, bi.live)); err != nil {
						bi.health = "UNHEALTHY"
					}
				}
				cl.Close()
			} else {
				bi.health = "unreachable"
			}
			svc.boxes = append(svc.boxes, bi)
		}
		services = append(services, svc)

		for _, b := range svc.boxes {
			t.ensureStream(name, b.addr, svc.colors)
		}
	}
	t.mu.Lock()
	t.services = services
	t.mu.Unlock()
	return nil
}

func (t *top) refreshLoop() {
	tk := time.NewTicker(10 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-t.quit:
			return
		case <-tk.C:
			_ = t.refresh()
		}
	}
}

// ensureStream follows both colors' journals on a box, forever, restarting
// after failures. Both colors, so a deploy's flip doesn't silence the pane.
func (t *top) ensureStream(svc, addr string, colors []int) {
	t.mu.Lock()
	if t.streams[addr] || len(colors) != 2 {
		t.mu.Unlock()
		return
	}
	t.streams[addr] = true
	t.mu.Unlock()

	go func() {
		for {
			select {
			case <-t.quit:
				return
			default:
			}
			cl, err := sshx.Dial(addr, t.key)
			if err == nil {
				cmd := fmt.Sprintf("journalctl -f -n 30 --no-pager -o short -u %s@%d -u %s@%d", svc, colors[0], svc, colors[1])
				_ = cl.Stream(cmd, "", func(line string) {
					t.push(logLine{svc: svc, box: addr, text: line})
				})
				cl.Close()
			}
			select {
			case <-t.quit:
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
}

func (t *top) push(l logLine) {
	t.mu.Lock()
	t.logs = append(t.logs, l)
	if len(t.logs) > logCap {
		t.logs = t.logs[len(t.logs)-logCap:]
	}
	t.mu.Unlock()
}

func (t *top) vitalsLoop() {
	tk := time.NewTicker(5 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-t.quit:
			return
		case <-tk.C:
		}
		t.mu.Lock()
		active := t.view == viewBox && t.selSvc < len(t.services) && t.selBox < len(t.services[t.selSvc].boxes)
		var addr string
		if active {
			addr = t.services[t.selSvc].boxes[t.selBox].addr
		}
		t.mu.Unlock()
		if !active {
			continue
		}
		cl, err := sshx.Dial(addr, t.key)
		if err != nil {
			continue
		}
		out, err := cl.Run(`echo "load|$(cut -d' ' -f1-3 /proc/loadavg) ($(nproc) cores)"; free -m | awk '/^Mem:/{printf "mem|%d / %dM (%d%%)\n",$3,$2,($3*100)/$2}'; df -h --output=pcent,target / /var 2>/dev/null | awk 'NR>1{printf "disk %s|%s\n",$2,$1}'; echo "up|$(uptime -p | sed s/^up\ //)"`)
		cl.Close()
		if err != nil {
			continue
		}
		var v []string
		for _, l := range strings.Split(out, "\n") {
			k, val, ok := strings.Cut(l, "|")
			if ok {
				v = append(v, fmt.Sprintf("%-9s %s", k, val))
			}
		}
		t.mu.Lock()
		t.vitals = v
		t.mu.Unlock()
	}
}

// matches applies the view scope plus the user filter.
func (t *top) matches(l logLine) bool {
	switch t.view {
	case viewService:
		if t.selSvc >= len(t.services) || l.svc != t.services[t.selSvc].name {
			return false
		}
	case viewBox:
		if t.selSvc >= len(t.services) || t.selBox >= len(t.services[t.selSvc].boxes) {
			return false
		}
		if l.svc != t.services[t.selSvc].name || l.box != t.services[t.selSvc].boxes[t.selBox].addr {
			return false
		}
	}
	if t.filter == "" {
		return true
	}
	f := strings.ToLower(t.filter)
	return strings.Contains(strings.ToLower(l.text), f) ||
		strings.Contains(strings.ToLower(l.svc), f) ||
		strings.Contains(l.box, f)
}

const (
	cReset = "\x1b[0m"
	cDim   = "\x1b[2m"
	cInv   = "\x1b[7m"
	cRed   = "\x1b[31m"
	cGreen = "\x1b[32m"
	cBold  = "\x1b[1m"
)

func (t *top) render() {
	t.mu.Lock()
	defer t.mu.Unlock()

	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w < 40 || h < 6 {
		return
	}
	rightW := w - leftW - 3

	var b strings.Builder
	b.WriteString("\x1b[H")

	line := func(s string) {
		b.WriteString(s)
		b.WriteString("\x1b[K\r\n")
	}

	// Header
	title := " hadi top · " + t.zone
	switch t.view {
	case viewService:
		title += " · " + t.services[t.selSvc].name
	case viewBox:
		title += " · " + t.services[t.selSvc].name + " · " + t.services[t.selSvc].boxes[t.selBox].addr
	}
	line(cBold + pad(title, w) + cReset)

	// Left pane rows
	var left []string
	switch t.view {
	case viewFleet:
		left = append(left, cDim+pad(" SERVICES", leftW)+cReset)
		for i, s := range t.services {
			health, hc := "ok", cGreen
			for _, bx := range s.boxes {
				if bx.health != "ok" {
					health, hc = bx.health, cRed
				}
			}
			if i == t.selSvc {
				row := fmt.Sprintf(" %-16s %2d box %s", trunc(s.name, 16), len(s.boxes), health)
				left = append(left, cInv+pad(row, leftW)+cReset)
			} else {
				base := fmt.Sprintf(" %-16s %2d box ", trunc(s.name, 16), len(s.boxes))
				left = append(left, pad(base, leftW-len(health)-1)+hc+health+cReset+" ")
			}
		}
	case viewService:
		left = append(left, cDim+pad(" BOXES · "+t.services[t.selSvc].name, leftW)+cReset)
		for i, bx := range t.services[t.selSvc].boxes {
			hc := cGreen
			if bx.health != "ok" {
				hc = cRed
			}
			if i == t.selBox {
				row := fmt.Sprintf(" %-17s @%d %s", trunc(bx.addr, 17), bx.live, bx.health)
				left = append(left, cInv+pad(row, leftW)+cReset)
			} else {
				base := fmt.Sprintf(" %-17s @%d ", trunc(bx.addr, 17), bx.live)
				left = append(left, pad(base, leftW-len(bx.health)-1)+hc+bx.health+cReset+" ")
			}
		}
	case viewBox:
		bx := t.services[t.selSvc].boxes[t.selBox]
		left = append(left, cDim+pad(" VITALS · "+bx.addr, leftW)+cReset)
		left = append(left, pad(fmt.Sprintf(" sha %s · live @%d", bx.sha, bx.live), leftW))
		left = append(left, "")
		for _, v := range t.vitals {
			left = append(left, pad(" "+v, leftW))
		}
	}

	// Right pane: filtered log tail
	var vis []logLine
	for _, l := range t.logs {
		if t.matches(l) {
			vis = append(vis, l)
		}
	}
	rows := h - 3
	end := len(vis) - t.logOff
	if end < 0 {
		end = 0
	}
	start := end - rows
	if start < 0 {
		start = 0
	}
	window := vis[start:end]

	for i := 0; i < rows; i++ {
		var l, r string
		if i < len(left) {
			l = left[i]
		} else {
			l = strings.Repeat(" ", leftW)
		}
		li := i - (rows - len(window))
		if li >= 0 && li < len(window) {
			ll := window[li]
			prefix := ""
			switch t.view {
			case viewFleet:
				prefix = cDim + "[" + trunc(ll.svc, 12) + "]" + cReset + " "
			case viewService:
				prefix = cDim + "[" + lastOctet(ll.box) + "]" + cReset + " "
			}
			r = prefix + trunc(ll.text, rightW-visLen(prefix))
		}
		line(l + cDim + " │ " + cReset + r)
	}

	// Footer
	foot := " enter drill · esc back · / filter · c clear · u/d scroll · q quit"
	if t.typing {
		foot = " filter: " + t.draft + "▌"
	} else if t.filter != "" {
		foot = fmt.Sprintf(" filter: %q (%d lines) · c to clear · %s", t.filter, len(vis), "q quit")
	}
	if t.logOff > 0 {
		foot += fmt.Sprintf(" · scrolled %d", t.logOff)
	}
	b.WriteString(cInv + pad(foot, w) + cReset + "\x1b[K")

	fmt.Print(b.String())
}

func pad(s string, w int) string {
	if visLen(s) >= w {
		return trunc(s, w)
	}
	return s + strings.Repeat(" ", w-visLen(s))
}

// visLen is length ignoring ANSI escapes.
func visLen(s string) int {
	n, in := 0, false
	for _, r := range s {
		if in {
			if r == 'm' {
				in = false
			}
			continue
		}
		if r == 0x1b {
			in = true
			continue
		}
		n++
	}
	return n
}

func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	n, in := 0, false
	for i, r := range s {
		if in {
			if r == 'm' {
				in = false
			}
			continue
		}
		if r == 0x1b {
			in = true
			continue
		}
		n++
		if n > w {
			return s[:i]
		}
	}
	return s
}

func lastOctet(addr string) string {
	if i := strings.LastIndex(addr, "."); i >= 0 && i < len(addr)-1 {
		return addr[i+1:]
	}
	return trunc(addr, 6)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = config.PeekZone // top shares context helpers with the rest of main
