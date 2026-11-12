package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ani07-05/slendertoken/internal/agent"
	"github.com/Ani07-05/slendertoken/internal/bifrost"
	"github.com/Ani07-05/slendertoken/internal/budget"
	"github.com/Ani07-05/slendertoken/internal/config"
	"github.com/Ani07-05/slendertoken/internal/gateway"
	"github.com/Ani07-05/slendertoken/internal/tui"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	config.LoadAll()
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "config":
			return config.Main(os.Args[2:])
		case "setup":
			return runSetup(os.Args[2:])
		case "bifrost":
			return runBifrost(os.Args[2:])
		case "provider":
			return bifrost.ProviderCommand(context.Background(), os.Args[2:], os.Stdout)
		}
	}
	fs := flag.NewFlagSet("slendertoken", flag.ContinueOnError)
	repoFlag := fs.String("repo", ".", "repo to work in")
	model := fs.String("model", "", "large model, written provider/model (for example openai/gpt-4o) (default: SLENDERTOKEN_MODEL or your saved pick)")
	small := fs.String("small", "", "first model, tried before the large one")
	noRoute := fs.Bool("no-route", false, "use only the large model")
	pick := fs.Bool("pick-model", false, "pick the large and first models and save them")
	limit := fs.Float64("budget", 0.50, "USD limit, persisted across runs")
	private := fs.Bool("private", false, "local model only, nothing leaves the machine")
	showVersion := fs.Bool("version", false, "print the version")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: slendertoken [flags] [task]")
		fmt.Fprintln(os.Stderr, "       slendertoken setup [--provider NAME] [--port N] [--budget USD] [--force]")
		fmt.Fprintln(os.Stderr, "       slendertoken provider [list|add [NAME]|remove NAME]")
		fmt.Fprintln(os.Stderr, "       slendertoken bifrost [start|stop|status|logs|path]")
		fmt.Fprintln(os.Stderr, "       slendertoken config [list|set|unset|edit|path]")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Println("slendertoken", version)
		return nil
	}
	task := strings.Join(fs.Args(), " ")
	if *pick {
		if _, err := gateway.PickModel(); err != nil {
			return err
		}
		if task == "" {
			return nil
		}
	}
	repo, err := filepath.Abs(*repoFlag)
	if err != nil {
		return err
	}
	_, privMarker := os.Stat(filepath.Join(repo, ".slendertoken-private"))
	isPrivate := *private || privMarker == nil
	var largeModel, smallModel string
	if !isPrivate {
		largeModel = *model
		if largeModel == "" {
			if task != "" {
				if largeModel, err = gateway.ResolveModel(""); err != nil {
					return err
				}
			} else if largeModel = os.Getenv("SLENDERTOKEN_MODEL"); largeModel == "" {
				largeModel = gateway.SavedModel()
			}
		}
		if !*noRoute {
			var flagSmall *string
			if fs.Lookup("small").Value.String() != "" {
				flagSmall = small
			}
			smallModel = gateway.ResolveSmall(flagSmall, largeModel)
		}
	}
	if !isPrivate {
		startManagedGateway()
	}
	b := budget.New(filepath.Join(repo, ".slendertoken", "budget.json"), *limit)
	if task == "" {
		return tui.Run(tui.Options{Repo: repo, Budget: b, Private: isPrivate, Model: largeModel, Small: smallModel, Version: version})
	}
	return oneShot(repo, b, isPrivate, largeModel, smallModel, task)
}

// startManagedGateway brings back the Bifrost that `slendertoken setup` installed if it is not running, for example
// after a reboot. A gateway the user runs themselves is left alone.
func startManagedGateway() {
	url := gateway.URL()
	if !bifrost.Managed() || bifrost.Healthy(url) {
		return
	}
	fmt.Fprintln(os.Stderr, "starting Bifrost...")
	if err := bifrost.Start(context.Background(), url); err != nil {
		fmt.Fprintln(os.Stderr, "could not start Bifrost:", err)
	}
}

func runSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	port := fs.Int("port", 0, "gateway port (default: first free port from 8080)")
	budget := fs.Float64("budget", 2, "daily budget in USD on the virtual key")
	force := fs.Bool("force", false, "redo setup even if a working gateway is configured")
	provider := fs.String("provider", "", "set up only this provider (openai, anthropic, gemini, groq, openrouter, ...)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return bifrost.Setup(context.Background(), bifrost.SetupOptions{Port: *port, BudgetUSD: *budget, Force: *force, Provider: *provider}, os.Stdout)
}

func runBifrost(args []string) error {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	url := gateway.URL()
	switch cmd {
	case "start":
		if err := bifrost.Start(context.Background(), url); err != nil {
			return err
		}
		fmt.Println("Bifrost running on", url)
	case "stop":
		if err := bifrost.Stop(); err != nil {
			return err
		}
		fmt.Println("Bifrost stopped")
	case "status":
		state := "stopped"
		if bifrost.Healthy(url) {
			state = "running"
		}
		fmt.Printf("%s at %s (managed by slendertoken: %v)\n", state, url, bifrost.Managed())
	case "logs":
		fmt.Println(bifrost.LogPath())
	case "path":
		fmt.Println(bifrost.Home())
	default:
		return errors.New("usage: slendertoken bifrost [start|stop|status|logs|path]")
	}
	return nil
}

func oneShot(repo string, b *budget.Budget, private bool, model, small, task string) error {
	if os.Getenv("BIFROST_VK") == "" && !private {
		fmt.Printf("note: BIFROST_VK is not set. Run `slendertoken config set BIFROST_VK <key>` (%s)\n", config.GlobalEnv)
	}
	ag, err := agent.New(agent.Options{
		Repo: repo, Model: model, Small: small, Budget: b, Private: private, Lean: true,
		Approve: func(text string) bool {
			fmt.Printf("\n[approve?]\n%s\n(y/N) ", text)
			var ans string
			fmt.Scanln(&ans)
			return strings.EqualFold(strings.TrimSpace(ans), "y")
		},
		Emit: func(e agent.Event) {
			switch e.Kind {
			case "wait":
				fmt.Printf("[rate limited, waiting %.0fs]\n", e.Seconds)
			case "escalate":
				fmt.Printf("[escalated to %s: %s]\n", e.To, e.Reason)
			}
		},
	})
	if err != nil {
		return err
	}
	result, err := ag.Run(task, 15)
	if err != nil {
		result = "error: " + err.Error()
	}
	fmt.Println(result)
	fmt.Println(ag.FP.Report(result, ag.Verified))
	path, _ := ag.FP.Save(filepath.Join(repo, ".slendertoken", "runs"), result, ag.Verified)
	fmt.Printf("saved %s  (budget spent $%.4f of $%.2f)\n", path, b.Spent(), b.Limit)
	return nil
}
