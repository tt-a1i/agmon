package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
)

func runLogs() error {
	if maybePrintCmdHelp("logs", os.Args[2:]) {
		return nil
	}
	opts, err := parseLogsArgs(os.Args[2:])
	if err != nil {
		return err
	}

	daemonPath := appdir.Path("daemon.log")
	emitPath := appdir.PathFor("emit.log", "emit.log")
	if opts.printPath {
		fmt.Println(daemonPath)
		fmt.Println(emitPath)
		return nil
	}

	path := daemonPath
	if opts.emit {
		path = emitPath
	}
	if err := printLastLogLines(os.Stdout, path, opts.lines); err != nil {
		return err
	}
	if opts.follow {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		return followLog(path, info.Size(), os.Stdout)
	}
	return nil
}

type logsOptions struct {
	lines     int
	follow    bool
	emit      bool
	printPath bool
}

func parseLogsArgs(args []string) (logsOptions, error) {
	opts := logsOptions{lines: 100}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--follow":
			opts.follow = true
		case "--emit":
			opts.emit = true
		case "--path":
			opts.printPath = true
		case "--lines":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--lines requires a value")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return opts, fmt.Errorf("invalid --lines value: %s", args[i])
			}
			opts.lines = n
		default:
			return opts, fmt.Errorf("unknown logs argument: %s", args[i])
		}
	}
	return opts, nil
}

func printLastLogLines(out io.Writer, path string, n int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	if len(data) == 0 || n == 0 {
		return nil
	}
	lines := bytes.Split(data, []byte("\n"))
	if n < len(lines) {
		lines = lines[len(lines)-n:]
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(out, string(line)); err != nil {
			return err
		}
	}
	return nil
}

func followLog(path string, offset int64, out io.Writer) error {
	for {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			_ = f.Close()
			return err
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			fmt.Fprintln(out, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			_ = f.Close()
			return err
		}
		if pos, err := f.Seek(0, io.SeekCurrent); err == nil {
			offset = pos
		}
		_ = f.Close()

		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Size() < offset {
			offset = 0
		}
		time.Sleep(200 * time.Millisecond)
	}
}
