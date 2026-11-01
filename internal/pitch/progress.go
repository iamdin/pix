package pitch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// progress prints the build steps. On a terminal a step is one line that shows its running time and ends with how long
// it took; elsewhere (a log, an agent) a step is printed once and a long compile adds a line every heartbeatEvery.
type progress struct {
	w      io.Writer
	tty    bool
	n      int
	header string
	start  time.Time
}

var (
	heartbeatEvery = 5 * time.Minute
	longCompile    = 10 * time.Minute // compiling usually takes 1–5 minutes
	errCompileTime = errors.New("compiling did not finish")
)

func newProgress(w io.Writer) *progress {
	f, ok := w.(*os.File)
	return &progress{w: w, tty: ok && isTerminal(f)}
}

func (p *progress) step(name string) {
	p.end()
	p.n++
	p.header, p.start = fmt.Sprintf("[%d/4] %s", p.n, name), time.Now()
	if p.tty {
		fmt.Fprint(p.w, p.header)
	} else {
		fmt.Fprintln(p.w, p.header)
	}
}

// end closes the current step, with its time on a terminal.
func (p *progress) end() {
	if p.header != "" && p.tty {
		fmt.Fprintf(p.w, "\r%s  %s\n", p.header, time.Since(p.start).Round(time.Second))
	}
	p.header = ""
}

func (p *progress) tick() {
	took := time.Since(p.start)
	note := ""
	if took >= longCompile {
		note = " (longer than usual)"
	}
	if p.tty {
		fmt.Fprintf(p.w, "\r%s  %s%s", p.header, took.Round(time.Second), note)
	} else {
		fmt.Fprintf(p.w, "      still compiling, %s%s\n", took.Round(time.Second), note)
	}
}

// compile runs the compiler while progress shows it is alive, and stops it after timeout (0: never).
func compile(ctx context.Context, r runner, timeout time.Duration, p *progress, dir string, log io.Writer, name string, args ...string) error {
	compileCtx, cancel := ctx, context.CancelFunc(func() {})
	if timeout > 0 {
		compileCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	every := heartbeatEvery
	if p.tty {
		every = time.Second
	}
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				p.tick()
			}
		}
	}()
	err := runner{compileCtx, r.env}.command(dir, log, name, args...)
	close(stop)
	<-stopped
	if err != nil && ctx.Err() == nil && errors.Is(compileCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w in %s (pitch build --timeout changes the limit)", errCompileTime, timeout)
	}
	return err
}
