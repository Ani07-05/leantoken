package bifrost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
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
	Provider  string  // limit setup to one provider id instead of asking or reading the environment
}

// Setup takes a machine from nothing to a working gateway: downloads Bifrost, writes its config with the user's provider
// keys and a budgeted virtual key, starts it, saves the settings slendertoken reads, and makes one tiny request to prove
// the whole path works.
func Setup(ctx context.Context, o SetupOptions, out io.Writer) error {
	say := func(format string, a ...any) { fmt.Fprintf(out, format+"\n", a...) }
	if o.BudgetUSD <= 0 {
		o.BudgetUSD = 2
	}
	if url := os.Getenv("BIFROST_URL"); !o.Force && os.Getenv("BIFROST_VK") != "" && url != "" && Healthy(url) {
		say("Already set up: gateway at %s is running and a virtual key is saved. Run with --force to redo it, or `slendertoken provider add` to add another provider.", url)
		return nil
	}

	providers, err := collectProviders(o.Provider, out)
	if err != nil {
		return err
	}
	names := make([]string, len(providers))
	for i, p := range providers {
		names[i] = p.ID
	}
	say("Keys received for: %s", strings.Join(names, ", "))

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
	setupToken := ExistingSetupToken()
	newSetupToken := setupToken == ""
	if newSetupToken {
		setupToken = NewSetupToken()
	}
	if err := WriteConfig(Spec{Providers: providers, VirtualKey: vk, SetupToken: setupToken, BudgetUSD: o.BudgetUSD}); err != nil {
		return err
	}
	say("Gateway config written ($%.2f per day budget)", o.BudgetUSD)
	if newSetupToken {
		say("Dashboard setup token (use once to create its admin account): %s", setupToken)
	}

	url := fmt.Sprintf("http://localhost:%d", port)
	if err := Start(ctx, url); err != nil {
		return err
	}
	say("Gateway running in the background on %s", url)

	for k, v := range map[string]string{"BIFROST_URL": url, "BIFROST_VK": vk} {
		v := v
		if err := config.SetValue(k, &v); err != nil {
			return err
		}
		os.Setenv(k, v)
	}
	model := chooseDefaults(providers)
	say("Settings saved to %s", config.GlobalEnv)

	switch err := verify(ctx, url, vk, model); {
	case errors.Is(err, errNoTestModel):
		say("Key saved. Pick a model when slendertoken opens; the first request will test it.")
	case err != nil:
		say("")
		say("The gateway is up but a test request failed: %v", err)
		say("Fix the key with: slendertoken setup --force")
		return errors.New("setup finished with a failing test request")
	default:
		say("Test request through the gateway worked")
	}
	say("")
	say("All set. Run: slendertoken")
	return nil
}

// chooseDefaults saves a first and a large model when the first provider has known ones and the saved choice no longer
// fits the configured providers. It returns the model to send the test request to, or "".
func chooseDefaults(providers []KeyedProvider) string {
	first, _ := ProviderByID(providers[0].ID)
	configured := map[string]bool{}
	for _, p := range providers {
		configured[p.ID] = true
	}
	if saved := gateway.SavedModel(); saved != "" && configured[strings.SplitN(saved, "/", 2)[0]] {
		return gateway.TestModel()
	}
	large, small := first.Large, first.Small
	_ = gateway.SaveModel(&large, &small)
	return small
}

// collectProviders gets the user's keys: from the environment when any provider's usual variable is set, otherwise by
// asking. Keys are typed with the input hidden.
func collectProviders(only string, out io.Writer) ([]KeyedProvider, error) {
	if only != "" {
		p, ok := ProviderByID(only)
		if !ok {
			return nil, fmt.Errorf("unknown provider %q. Choose from: %s", only, providerIDs())
		}
		key, err := keyFor(p, out)
		if err != nil {
			return nil, err
		}
		return []KeyedProvider{{p.ID, key}}, nil
	}
	if env := FromEnvironment(); len(env) > 0 {
		names := make([]string, len(env))
		for i, p := range env {
			names[i] = p.ID + " (" + providerEnv(p.ID) + ")"
		}
		fmt.Fprintf(out, "Using the API keys found in your environment: %s\n", strings.Join(names, ", "))
		fmt.Fprintln(out, "To use only one provider, run: slendertoken setup --provider NAME")
		return env, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, fmt.Errorf("no terminal to ask for a key: set the API key variable for your provider (for example OPENAI_API_KEY or ANTHROPIC_API_KEY) and run again")
	}
	in := bufio.NewReader(os.Stdin)
	var picked []KeyedProvider
	for {
		p, err := askProvider(in, out)
		if err != nil {
			return nil, err
		}
		key, err := keyFor(p, out)
		if err != nil {
			return nil, err
		}
		picked = append(picked, KeyedProvider{p.ID, key})
		fmt.Fprint(out, "Add another provider? [y/N] ")
		if line, _ := in.ReadString('\n'); !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y") {
			return picked, nil
		}
	}
}

func providerEnv(id string) string {
	p, _ := ProviderByID(id)
	return p.EnvVar
}

func providerIDs() string {
	ids := make([]string, len(Providers))
	for i, p := range Providers {
		ids[i] = p.ID
	}
	return strings.Join(ids, ", ")
}

func askProvider(in *bufio.Reader, out io.Writer) (Provider, error) {
	fmt.Fprintln(out, "Which provider do you want to use? Bring your own key.")
	for i, p := range Providers {
		fmt.Fprintf(out, "%3d. %s\n", i+1, p.Name)
	}
	fmt.Fprintln(out, "Others (Bedrock, Azure, Vertex, local models) can be added in Bifrost's own config.")
	for {
		fmt.Fprint(out, "Number or name: ")
		line, err := in.ReadString('\n')
		line = strings.ToLower(strings.TrimSpace(line))
		if n, convErr := strconv.Atoi(line); convErr == nil && n >= 1 && n <= len(Providers) {
			return Providers[n-1], nil
		}
		if p, ok := ProviderByID(line); ok {
			return p, nil
		}
		if err != nil {
			return Provider{}, errors.New("no provider chosen")
		}
		fmt.Fprintln(out, "not in the list")
	}
}

// keyFor reads the provider's key from its environment variable, or asks with the input hidden.
func keyFor(p Provider, out io.Writer) (string, error) {
	if k := strings.TrimSpace(os.Getenv(p.EnvVar)); k != "" {
		return k, nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("no terminal to ask for the %s key: set %s and run again", p.Name, p.EnvVar)
	}
	fmt.Fprintf(out, "%s API key, input hidden: ", p.Name)
	raw, err := term.ReadPassword(fd)
	fmt.Fprintln(out)
	if err != nil {
		return "", err
	}
	k := strings.TrimSpace(string(raw))
	if k == "" {
		return "", errors.New("no key entered")
	}
	return k, nil
}

var errNoTestModel = errors.New("no model to test with")

// verify sends one tiny chat request through the gateway with the virtual key. A rejected key is an error; a model the
// provider does not know is not, since the default is only a guess.
func verify(ctx context.Context, url, vk, model string) error {
	if model == "" {
		return errNoTestModel
	}
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
	if (resp.StatusCode == 400 || resp.StatusCode == 404) && strings.Contains(strings.ToLower(msg), "model") {
		return errNoTestModel
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg[:min(len(msg), 200)])
}

// ProviderCommand runs `slendertoken provider list|add|remove`.
func ProviderCommand(ctx context.Context, args []string, out io.Writer) error {
	cmd, rest := "list", []string(nil)
	if len(args) > 0 {
		cmd, rest = args[0], args[1:]
	}
	spec, err := LoadSpec()
	if err != nil {
		return err
	}
	switch {
	case cmd == "list":
		for _, p := range spec.Providers {
			fmt.Fprintln(out, p.ID)
		}
		return nil
	case cmd == "add":
		only := ""
		if len(rest) > 0 {
			only = rest[0]
		}
		var added []KeyedProvider
		if only == "" {
			in := bufio.NewReader(os.Stdin)
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return errors.New("usage: slendertoken provider add <name>   (names: " + providerIDs() + ")")
			}
			p, err := askProvider(in, out)
			if err != nil {
				return err
			}
			only = p.ID
		}
		var err error
		if added, err = collectProviders(only, out); err != nil {
			return err
		}
		spec = spec.With(added[0])
	case cmd == "remove" && len(rest) == 1:
		if len(spec.Providers) <= 1 {
			return errors.New("cannot remove the only provider; add another first")
		}
		spec = spec.Without(rest[0])
	default:
		return errors.New("usage: slendertoken provider [list | add [name] | remove <name>]")
	}
	if err := WriteConfig(spec); err != nil {
		return err
	}
	url := os.Getenv("BIFROST_URL")
	if url == "" {
		url = "http://localhost:8080"
	}
	_ = Stop()
	time.Sleep(500 * time.Millisecond)
	if err := Start(ctx, url); err != nil {
		return err
	}
	fmt.Fprintln(out, "Done. Gateway restarted with the new provider list. Pick a model with ctrl+o in the app.")
	return nil
}
