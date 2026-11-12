package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/Ani07-05/slendertoken/internal/config"
	"github.com/Ani07-05/slendertoken/internal/gateway"
)

type SetupOptions struct {
	Port      int     // 0 picks the first free port from 8080
	BudgetUSD float64 // daily budget on the virtual key
	Force     bool    // redo setup even if a working gateway is already configured
}

const defaultLarge = "groq/openai/gpt-oss-120b"

// Setup takes a machine from nothing to a working gateway: downloads Bifrost, writes its config with the user's Groq
// key and a budgeted virtual key, starts it, saves the settings slendertoken reads, and makes one tiny request to prove
// the whole path works.
func Setup(ctx context.Context, o SetupOptions, out io.Writer) error {
	say := func(format string, a ...any) { fmt.Fprintf(out, format+"\n", a...) }
	if o.BudgetUSD <= 0 {
		o.BudgetUSD = 2
	}
	if url := os.Getenv("BIFROST_URL"); !o.Force && os.Getenv("BIFROST_VK") != "" && url != "" && Healthy(url) {
		say("Already set up: gateway at %s is running and a virtual key is saved. Run with --force to redo it.", url)
		return nil
	}

	key, err := groqKey(out)
	if err != nil {
		return err
	}
	say("Groq key received")

	bin, err := Install(ctx, func(m string) { say("  %s", m) })
	if err != nil {
		return err
	}
	say("Bifrost ready at %s", bin)

	// the old gateway must stop first: it holds the port and has the old config in memory
	_ = Stop()
	time.Sleep(500 * time.Millisecond)
	port := o.Port
	if port == 0 {
		port = FreePort()
	}
	vk := ExistingVirtualKey()
	if vk == "" {
		vk = NewVirtualKey()
	}
	if err := WriteConfig(Spec{GroqKey: key, VirtualKey: vk, BudgetUSD: o.BudgetUSD}); err != nil {
		return err
	}
	say("Gateway config written (Groq provider, $%.2f per day budget)", o.BudgetUSD)

	url := fmt.Sprintf("http://localhost:%d", port)
	if err := Start(ctx, url); err != nil {
		return err
	}
	say("Gateway running on %s", url)

	large, small := defaultLarge, gateway.DefaultSmall
	for k, v := range map[string]string{"BIFROST_URL": url, "BIFROST_VK": vk} {
		v := v
		if err := config.SetValue(k, &v); err != nil {
			return err
		}
		os.Setenv(k, v)
	}
	if err := gateway.SaveModel(&large, &small); err != nil {
		return err
	}
	say("Settings saved to %s", config.GlobalEnv)

	if err := verify(ctx, url, vk, small); err != nil {
		say("")
		say("The gateway is up but a test request failed: %v", err)
		say("Fix the key with: slendertoken setup --force")
		return errors.New("setup finished with a failing test request")
	}
	say("Test request through the gateway worked")
	say("")
	say("All set. Run: slendertoken")
	return nil
}

func groqKey(out io.Writer) (string, error) {
	if k := strings.TrimSpace(os.Getenv("GROQ_API_KEY")); k != "" {
		return k, nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("no terminal to ask for the Groq key: set GROQ_API_KEY and run again (get a free key at https://console.groq.com/keys)")
	}
	fmt.Fprint(out, "Groq API key (free at https://console.groq.com/keys), input hidden: ")
	raw, err := term.ReadPassword(fd)
	fmt.Fprintln(out)
	if err != nil {
		return "", err
	}
	k := strings.TrimSpace(string(raw))
	if k == "" {
		return "", errors.New("no key entered")
	}
	if !strings.HasPrefix(k, "gsk_") {
		fmt.Fprintln(out, "note: Groq keys usually start with gsk_; continuing anyway")
	}
	return k, nil
}

// verify sends one tiny chat request through the gateway with the virtual key.
func verify(ctx context.Context, url, vk, model string) error {
	body, _ := json.Marshal(map[string]any{"model": model, "max_tokens": 8, "messages": []any{map[string]string{"role": "user", "content": "hi"}}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-bf-vk", vk)
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		return nil
	}
	raw, _ := io.ReadAll(resp.Body)
	var e struct{ Error struct{ Message string } }
	msg := string(raw)
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Message
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg[:min(len(msg), 200)])
}
