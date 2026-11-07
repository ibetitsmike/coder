// Command testplugin is a plugin used by the sandbox and pluginhost tests.
// OnEvent selects its behavior from the text of the last message.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/coder/coder/v2/codersdk/x/pluginsdk"
)

var (
	counter int
	hoard   [][]byte
)

func init() {
	pluginsdk.Register(pluginsdk.Plugin{
		Manifest: pluginsdk.Manifest{
			Name:        "testplugin",
			Version:     "1.2.3",
			Events:      []string{"chat.status"},
			Chats:       []string{"ext"},
			HTTPOrigins: []string{"https://example.com"},
			ConfigKeys:  []string{"greeting"},
			TickSeconds: 30,
			Webhook:     true,
		},
		OnTick:  onTick,
		OnEvent: onEvent,
		OnHTTP:  onHTTP,
	})
}

func main() {}

func onTick(h pluginsdk.Host) error {
	greeting := h.Config("greeting")
	fmt.Println("tick stdout")
	chatID, err := h.ChatSend("ext-1", greeting+" from tick")
	if err != nil {
		return fmt.Errorf("chat send: %w", err)
	}
	h.Logf("tick sent to %s", chatID)
	return nil
}

func onEvent(h pluginsdk.Host, ev pluginsdk.ChatEvent) error {
	var text string
	if len(ev.Messages) > 0 {
		text = ev.Messages[len(ev.Messages)-1].Text
	}
	cmd, arg, _ := strings.Cut(text, " ")
	switch cmd {
	case "echo":
		_, err := h.ChatSend(ev.ExternalID, "echo: "+arg)
		return err
	case "count":
		counter++
		_, err := h.ChatSend(ev.ExternalID, fmt.Sprint(counter))
		return err
	case "loop":
		for {
			counter++
		}
	case "spin":
		// The log call tells the host that the loop is about to start.
		h.Logf("spinning")
		for {
			counter++
		}
	case "alloc":
		for {
			hoard = append(hoard, make([]byte, 16<<20))
		}
	case "panic":
		panic("boom")
	case "error":
		return errors.New("handler failed")
	case "log":
		fmt.Print(strings.Repeat("x", 4096))
		return nil
	case "sandbox":
		report, err := json.Marshal(probeSandbox())
		if err != nil {
			return err
		}
		_, err = h.ChatSend(ev.ExternalID, string(report))
		return err
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// probeSandbox reports the result of each escape attempt. A nil error means
// the attempt succeeded.
func probeSandbox() map[string]string {
	report := map[string]string{}
	record := func(name string, err error) {
		if err == nil {
			report[name] = "ok"
			return
		}
		report[name] = "error: " + err.Error()
	}
	_, err := os.ReadFile("/etc/passwd")
	record("read_file", err)
	_, err = os.ReadDir(".")
	record("read_dir", err)
	report["env"] = strings.Join(os.Environ(), ",")
	report["getenv"] = os.Getenv("HOME") + os.Getenv("PATH")
	report["args"] = strings.Join(os.Args, ",")
	conn, err := net.Dial("tcp", "127.0.0.1:80")
	if conn != nil {
		_ = conn.Close()
	}
	record("dial", err)
	return report
}

func onHTTP(h pluginsdk.Host, req pluginsdk.HTTPRequest) pluginsdk.HTTPResponse {
	body, _ := json.Marshal(map[string]string{
		"method":   req.Method,
		"url":      req.URL,
		"body":     string(req.Body),
		"x-test":   req.Header.Get("X-Test"),
		"greeting": h.Config("greeting"),
	})
	return pluginsdk.HTTPResponse{
		StatusCode: 201,
		Header:     map[string][]string{"Content-Type": {"application/json"}},
		Body:       body,
	}
}
