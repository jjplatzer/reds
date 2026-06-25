package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const defaultSmesJar = "server/smes/target/faascope-stdds-1.0-SNAPSHOT.jar"

type smesConsumer struct {
	mu sync.Mutex

	cmd      *exec.Cmd
	done     chan struct{}
	stopping bool
}

func (c *smesConsumer) Start(airport string) error {
	if c == nil {
		return fmt.Errorf("SMES consumer is unavailable")
	}

	airport = strings.ToUpper(strings.TrimSpace(airport))
	if airport == "" {
		return fmt.Errorf("cannot start SMES consumer without an airport")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil {
		return nil
	}

	jar := os.Getenv("REDS_SMES_JAR")
	if jar == "" {
		jar = defaultSmesJar
	}
	if _, err := os.Stat(jar); err != nil {
		return fmt.Errorf("find SMES consumer jar %s: %w", jar, err)
	}

	cmd := exec.Command("java", "-jar", jar)
	cmd.Env = environmentWith(os.Environ(), "INITIAL_AIRPORT", airport)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start SMES consumer: %w", err)
	}

	done := make(chan struct{})
	c.cmd = cmd
	c.done = done
	c.stopping = false
	go c.wait(cmd, done)
	return nil
}

func (c *smesConsumer) Stop() {
	if c == nil {
		return
	}

	c.mu.Lock()
	cmd := c.cmd
	done := c.done
	if cmd == nil {
		c.mu.Unlock()
		return
	}
	c.stopping = true
	c.mu.Unlock()

	if cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	select {
	case <-done:
		return
	case <-time.After(500 * time.Millisecond):
	}

	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
	}
}

func (c *smesConsumer) wait(cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()

	c.mu.Lock()
	stopping := c.stopping
	if c.cmd == cmd {
		c.cmd = nil
		c.done = nil
	}
	c.mu.Unlock()

	close(done)
	if !stopping {
		fmt.Fprintf(os.Stderr, "reds: SMES consumer stopped: %v\n", err)
	}
}

func environmentWith(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+value)
}
