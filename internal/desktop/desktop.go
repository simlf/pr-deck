// Package desktop opens URLs in the browser and copies text to the
// clipboard, using whatever the platform provides.
package desktop

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Open opens url with cmd (when configured) or the platform opener.
func Open(url string, cmd []string) error {
	if url == "" {
		return fmt.Errorf("no URL to open")
	}
	if len(cmd) > 0 {
		return exec.Command(cmd[0], append(cmd[1:], url)...).Start()
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// Copy puts text on the clipboard: cmd (when configured), else pbcopy,
// wl-copy, xclip or xsel, else an OSC 52 escape to the controlling terminal
// (works over SSH and in most multiplexers).
func Copy(text string, cmd []string) error {
	candidates := [][]string{}
	if len(cmd) > 0 {
		candidates = append(candidates, cmd)
	}
	candidates = append(candidates, []string{"pbcopy"}, []string{"wl-copy"},
		[]string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		p := exec.Command(c[0], c[1:]...)
		p.Stdin = strings.NewReader(text)
		if err := p.Run(); err == nil {
			return nil
		}
	}
	return osc52(text)
}

func osc52(text string) error {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("no clipboard tool and no terminal for OSC 52")
	}
	defer tty.Close()
	_, err = fmt.Fprintf(tty, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
	return err
}
