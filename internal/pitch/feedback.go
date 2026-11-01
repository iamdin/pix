package pitch

import (
	"bufio"
	"bytes"
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

// writeFeedback writes what a GitHub issue about a failed build needs to stage/feedback.md, with the home directory
// replaced by ~. Nothing is sent: `pitch feedback` sends it once the user agrees.
func writeFeedback(stage, step string, took time.Duration, failure error, extensions []Extension) (string, error) {
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
	file := filepath.Join(stage, "feedback.md")
	return file, os.WriteFile(file, []byte(text), 0600)
}

// failure adds to a failed build's error where its log and feedback are, then, last, what an agent should do.
func failure(err error, stage, step string, took time.Duration, extensions []Extension) error {
	log := filepath.Join(stage, "build.log")
	file, writeErr := writeFeedback(stage, step, took, err, extensions)
	if writeErr != nil {
		return fmt.Errorf("%w\nLog: %s", err, log)
	}
	if step == "" {
		step = "setup"
	}
	cause := ""
	if errors.Is(err, errCompileTime) {
		cause = " A compile this long usually means an extension with a very large function: find it by leaving extensions out one at a time with {\"source\": \"...\", \"extensions\": []} in the Pi settings."
	}
	return fmt.Errorf("%w\nLog: %s\nSend feedback: pitch feedback %s\n\n"+
		"If you are an AI agent: the build failed at %q after %s; the user's current Pi is unchanged.%s "+
		"Show the feedback file to the user and send it only with their consent: pitch feedback -y %s",
		err, log, file, step, took.Round(time.Second), cause, file)
}

// feedback files the feedback from writeFeedback as an issue with the GitHub CLI, after the user confirms (yes: already did).
func feedback(file string, yes bool, stdin *os.File, stdout io.Writer) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	title, body, _ := strings.Cut(string(data), "\n")
	title = strings.TrimPrefix(title, "# ")
	link := "https://github.com/" + issueRepo + "/issues/new?" + url.Values{"title": {title}, "body": {truncate(body, 6000)}}.Encode()
	if !yes {
		unconfirmed := fmt.Errorf("not sent: no one confirmed\n\nIf you are an AI agent: show %s to the user and send it only with their consent: pitch feedback -y %s", file, file)
		if !isTerminal(stdin) || isDevNull(stdin) {
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
	// Without a logged-in GitHub CLI the user submits a prefilled form; it is not sent until they do.
	byHand := "Not sent yet: open this link in a browser signed in to GitHub and submit the form:\n" + link + "\n"
	gh, err := exec.LookPath("gh")
	if err != nil {
		fmt.Fprint(stdout, byHand)
		return nil
	}
	if exec.Command(gh, "auth", "status", "--hostname", "github.com").Run() != nil {
		fmt.Fprintf(stdout, "%s(or log in with gh auth login and send it again)\n", byHand)
		return nil
	}
	var out, stderr bytes.Buffer
	cmd := exec.Command(gh, "issue", "create", "--repo", issueRepo, "--title", title, "--body-file", "-")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(body), &out, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("not sent: gh issue create: %v\n%s\nOpen this link in a browser signed in to GitHub and submit the form instead:\n%s", err, strings.TrimSpace(stderr.String()), link)
	}
	fmt.Fprintf(stdout, "Sent: %s\n", strings.TrimSpace(out.String()))
	return nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "\n…(truncated; the full feedback is in the file)"
	}
	return s
}

func isDevNull(f *os.File) bool {
	a, errA := f.Stat()
	b, errB := os.Stat(os.DevNull)
	return errA == nil && errB == nil && os.SameFile(a, b)
}
