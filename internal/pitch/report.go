package pitch

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const issueRepo = "iamdin/pitch"

// heartbeatEvery is how often a long compile says it is still running.
var heartbeatEvery = time.Minute

// compile runs the compiler, says every heartbeatEvery that it is still running, and stops it after timeout (0: never).
func compile(ctx context.Context, r runner, timeout time.Duration, stdout io.Writer, dir string, log io.Writer, name string, args ...string) error {
	compileCtx, cancel := ctx, context.CancelFunc(func() {})
	if timeout > 0 {
		compileCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		start, warned := time.Now(), false
		tick := time.NewTicker(heartbeatEvery)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				fmt.Fprintf(stdout, "      still compiling, %s\n", time.Since(start).Round(time.Second))
				// The compiler logs nothing while it works, so say once what a long compile usually means.
				if !warned && time.Since(start) >= 10*heartbeatEvery {
					warned = true
					fmt.Fprintln(stdout, "      longer than usual (1–5 minutes): an extension with a very large function can take much longer")
				}
			}
		}
	}()
	err := runner{compileCtx, r.env}.command(dir, log, name, args...)
	close(stop)
	<-stopped
	if err != nil && ctx.Err() == nil && errors.Is(compileCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("compiling did not finish in %s (change the limit with pitch build --timeout); an extension with a very large function can take this long: leave one out with {\"source\": \"...\", \"extensions\": []} in the settings to find it", timeout)
	}
	return err
}

// writeReport writes what a GitHub issue about a failed build needs to stage/issue.md, with the home directory
// replaced by ~. Nothing is sent: `pitch report` sends it once the user agrees.
func writeReport(stage, step string, took time.Duration, failure error, extensions []Extension) (string, error) {
	var b strings.Builder
	what := step
	if what == "" {
		what = "before it started"
	}
	fmt.Fprintf(&b, "# pitch build failed: %s\n\n- pitch: %s\n- platform: %s\n- step: %s\n- after: %s\n- extensions:", what, Version, platform, what, took.Round(time.Second))
	if len(extensions) == 0 {
		b.WriteString(" none")
	}
	for _, e := range extensions {
		fmt.Fprintf(&b, "\n  - %s %s (%s)", e.Name, e.Version, e.Source)
	}
	fmt.Fprintf(&b, "\n\n## Error\n\n```\n%s\n```\n", failure)
	if data, err := os.ReadFile(filepath.Join(stage, "build.log")); err == nil && len(data) > 0 {
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		lines = lines[max(0, len(lines)-40):]
		fmt.Fprintf(&b, "\n## Last lines of build.log\n\n```\n%s\n```\n", strings.Join(lines, "\n"))
	}
	text := b.String()
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		text = strings.ReplaceAll(text, home, "~")
	}
	file := filepath.Join(stage, "issue.md")
	return file, os.WriteFile(file, []byte(text), 0600)
}

func reportHint(file string) string {
	return fmt.Sprintf("A report for a GitHub issue is in %s; nothing has been sent.\nRead it and remove anything private, then send it with: pitch report %s\nAgents: show the report to the user, and run `pitch report -y %s` only after they agree.", file, file, file)
}

// report files a report from writeReport as an issue with the GitHub CLI, after the user confirms (yes: already did).
func report(file string, yes bool, stdin *os.File, stdout io.Writer) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	title, body, _ := strings.Cut(string(data), "\n")
	title = strings.TrimPrefix(title, "# ")
	link := "https://github.com/" + issueRepo + "/issues/new?" + url.Values{"title": {title}, "body": {truncate(body, 6000)}}.Encode()
	if !yes {
		unconfirmed := fmt.Errorf("not sent: show %s to the user, and once they agree run pitch report -y %s", file, file)
		if !isTerminal(stdin) {
			return unconfirmed
		}
		fmt.Fprintf(stdout, "%s\nSend this as a public issue to https://github.com/%s? [y/N] ", data, issueRepo)
		answer, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && answer == "" { // no one to answer, e.g. stdin is /dev/null
			fmt.Fprintln(stdout)
			return unconfirmed
		}
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
			fmt.Fprintln(stdout, "Not sent.")
			return nil
		}
	}
	retry := fmt.Sprintf("then run pitch report -y %s again; or open this link and submit the form yourself:\n%s", file, link)
	gh, err := exec.LookPath("gh")
	if err != nil {
		return fmt.Errorf("not sent: the GitHub CLI (gh) is not installed. Install it (https://cli.github.com, e.g. brew install gh) and log in with gh auth login, %s", retry)
	}
	if exec.Command(gh, "auth", "status", "--hostname", "github.com").Run() != nil {
		return fmt.Errorf("not sent: gh is not logged in to github.com. The user logs in with gh auth login (it is interactive; an agent asks the user to run it), %s", retry)
	}
	var out, stderr bytes.Buffer
	cmd := exec.Command(gh, "issue", "create", "--repo", issueRepo, "--title", title, "--body-file", "-")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(body), &out, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("not sent: gh issue create: %v\n%s\nFix that, %s", err, strings.TrimSpace(stderr.String()), retry)
	}
	fmt.Fprintf(stdout, "Sent: %s\n", strings.TrimSpace(out.String()))
	return nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "\n…(truncated; the full report is in the file)"
	}
	return s
}
