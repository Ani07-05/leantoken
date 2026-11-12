// Command bench measures slendertoken's token controls: the same model on the same tasks, lean and naive.
//
//	go run ./bench run --model groq/openai/gpt-oss-120b   # writes bench/results.json
//	go run ./bench chart                                  # renders bench/token-efficiency.svg from it
//
// Both arms go through Bifrost. Token counts are the API's own usage, not estimates. The naive arm keeps whole
// files, unclipped tool output and the full transcript across turns. The coding tasks fix a seeded bug in a
// small Go project and pass when its `go test ./...` is green afterwards.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ani07-05/slendertoken/internal/config"
)

// dir is where results.json and the chart live, relative to the repo root that `go run ./bench` runs from.
const dir = "bench"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "run":
		return runBench(args, out)
	case "chart":
		fs := flag.NewFlagSet("chart", flag.ContinueOnError)
		in := fs.String("in", filepath.Join(dir, "results.json"), "results file")
		dst := fs.String("out", filepath.Join(dir, "token-efficiency.svg"), "SVG to write")
		if err := fs.Parse(args); err != nil {
			return err
		}
		r, err := readResults(*in)
		if err != nil {
			return err
		}
		svg, err := chart(r, filepath.ToSlash(*in))
		if err != nil {
			return err
		}
		if err := os.WriteFile(*dst, []byte(svg), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(out, "wrote", *dst)
		return nil
	}
	return fmt.Errorf("unknown command %q: use run or chart", cmd)
}

func readResults(path string) (results, error) {
	var r results
	data, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

func writeResults(path string, r results) error {
	data, err := json.MarshalIndent(r, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func runBench(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	model := fs.String("model", "groq/openai/gpt-oss-120b", "provider/model, used by both arms")
	pause := fs.Duration("pause", 45*time.Second, "wait between runs, lets the per-minute token budget refill")
	only := fs.String("only", "", "run only coding or session")
	bugList := fs.String("bugs", strings.Join(bugNames(), ","), "comma-separated coding tasks")
	arms := fs.String("arms", "lean,naive", "comma-separated arms to run")
	steps := fs.Int("steps", 0, "step limit per task, 0 for none (as in the app)")
	dst := fs.String("out", filepath.Join(dir, "results.json"), "results file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *only != "" && *only != "coding" && *only != "session" {
		return fmt.Errorf("--only must be coding or session, not %q", *only)
	}
	var chosen []string
	for _, b := range strings.Split(*bugList, ",") {
		if b = strings.TrimSpace(b); b == "" {
			continue
		}
		if _, ok := findBug(b); !ok {
			return fmt.Errorf("unknown bug %q (have %s)", b, strings.Join(bugNames(), ", "))
		}
		chosen = append(chosen, b)
	}
	var armList []string
	for _, a := range strings.Split(*arms, ",") {
		switch a = strings.TrimSpace(a); a {
		case "lean", "naive":
			armList = append(armList, a)
		case "":
		default:
			return fmt.Errorf("unknown arm %q: use lean and/or naive", a)
		}
	}
	config.LoadAll()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	r := runner{model: *model, pause: *pause, steps: *steps, ctx: ctx}
	res := results{Model: *model, Coding: map[string]map[string]row{}, Session: map[string][]row{}}
	first := true
	wait := func() {
		if !first {
			r.sleep()
		}
		first = false
	}
	for _, arm := range armList {
		lean := arm == "lean"
		if *only != "session" {
			res.Coding[arm] = map[string]row{}
			for _, b := range chosen {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				wait()
				row, err := r.coding(b, lean)
				if err != nil {
					return err
				}
				res.Coding[arm][b] = row
				fmt.Fprintf(out, "[coding/%s] %s: %s tokens, %d calls, passed=%v %s\n", arm, b, commas(row.Tokens), row.Calls, row.Passed, row.Error)
				if err := writeResults(*dst, res); err != nil {
					return err
				}
			}
		}
		if *only != "coding" {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			wait()
			rows, err := r.session(lean)
			res.Session[arm] = rows
			for i, row := range rows {
				fmt.Fprintf(out, "[session/%s] turn %d: %s tokens, passed=%v %s\n", arm, i+1, commas(row.Tokens), row.Passed, row.Error)
			}
			if werr := writeResults(*dst, res); werr != nil {
				return werr
			}
			if err != nil {
				return err
			}
		}
	}
	if ctx.Err() != nil {
		return errors.New("interrupted; partial results saved")
	}
	fmt.Fprintln(out, "saved", *dst)
	return nil
}
