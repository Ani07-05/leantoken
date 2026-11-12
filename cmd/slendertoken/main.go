package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ani07-05/slendertoken/internal/agent"
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
	if len(os.Args) > 1 && os.Args[1] == "config" {
		return config.Main(os.Args[2:])
	}
	fs := flag.NewFlagSet("slendertoken", flag.ContinueOnError)
	repoFlag := fs.String("repo", ".", "repo to work in")
	model := fs.String("model", "", "large Groq model via Bifrost (default: SLENDERTOKEN_MODEL or your saved pick)")
	small := fs.String("small", "", "first model, tried before the large one (default: "+gateway.DefaultSmall+")")
	noRoute := fs.Bool("no-route", false, "use only the large model")
	pick := fs.Bool("pick-model", false, "pick the large and first models and save them")
	limit := fs.Float64("budget", 0.50, "USD limit, persisted across runs")
	private := fs.Bool("private", false, "local model only, nothing leaves the machine")
	showVersion := fs.Bool("version", false, "print the version")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: slendertoken [flags] [task]")
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
			smallModel = gateway.ResolveSmall(flagSmall)
		}
	}
	b := budget.New(filepath.Join(repo, ".slendertoken", "budget.json"), *limit)
	if task == "" {
		return tui.Run(tui.Options{Repo: repo, Budget: b, Private: isPrivate, Model: largeModel, Small: smallModel, Version: version})
	}
	return oneShot(repo, b, isPrivate, largeModel, smallModel, task)
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
