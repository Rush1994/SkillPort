// Package cli implements the command boundary. Skill content is never executed.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/Rush1994/SkillPort/internal/errs"
	"github.com/spf13/cobra"
)

type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Envelope struct {
	SchemaVersion int    `json:"schemaVersion"`
	Success       bool   `json:"success"`
	Data          any    `json:"data"`
	Error         *Error `json:"error"`
}

// Run creates a fresh command tree for each invocation, including tests.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, build BuildInfo) int {
	var format string
	var quiet, nonInteractive bool
	root := &cobra.Command{
		Use: "skillport", Short: "Sync editable Skill directories with Harbor (development build)",
		Long:         "Sync editable Skill directories with Harbor.\n\nDevelopment preview: publish unique tags; use Harbor immutable tags for strict concurrency protection. Skills are never executed.",
		SilenceUsage: true, SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetIn(os.Stdin)
	root.SetArgs(args)
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().StringVar(&format, "format", "text", "Output format: text or json")
	root.PersistentFlags().BoolVar(&quiet, "quiet", false, "Suppress progress diagnostics; preserve command results")
	root.PersistentFlags().BoolVar(&nonInteractive, "non-interactive", false, "Never prompt for input")
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if format != "text" && format != "json" {
			return fmt.Errorf("invalid output format")
		}
		return nil
	}
	root.RunE = func(cmd *cobra.Command, args []string) error { return cmd.Help() }
	root.Args = cobra.NoArgs
	app := &application{format: &format, configPath: "", root: root}
	app.register()
	root.AddCommand(&cobra.Command{
		Use: "version", Short: "Show build and platform information", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			build.GoVersion, build.OS, build.Arch = runtime.Version(), runtime.GOOS, runtime.GOARCH
			if format == "json" {
				return json.NewEncoder(stdout).Encode(Envelope{SchemaVersion: 1, Success: true, Data: build})
			}
			_, err := fmt.Fprintf(stdout, "skillport %s (%s/%s; %s; commit %s; built %s)\n", build.Version, build.OS, build.Arch, build.GoVersion, build.Commit, build.BuildDate)
			return err
		},
	})
	if err := root.ExecuteContext(ctx); err != nil {
		// Cobra errors may quote user arguments. Do not echo untrusted arguments,
		// which may contain credentials, until typed/redacted errors are introduced.
		failure := &Error{Code: 2, Message: "Command failed; check command names and flags with --help."}
		var safe *errs.Error
		if errors.As(err, &safe) {
			failure = &Error{Code: safe.Code, Message: safe.Message}
		}
		if format == "json" {
			_ = json.NewEncoder(stdout).Encode(Envelope{SchemaVersion: 1, Data: app.failureData, Error: failure})
		} else {
			_, _ = fmt.Fprintln(stderr, failure.Message)
		}
		return failure.Code
	}
	return 0
}
