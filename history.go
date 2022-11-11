package sshw

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var dfhistfile = make(map[string]string)

func init() {
	hdir, err := os.UserHomeDir()
	if err != nil {
		l.Errorf("get home dir fail : %s", err)
		return
	}

	dfhistfile = map[string]string{
		"zsh":  hdir + "/.zsh_history",
		"bash": hdir + "/.bash_history",
		"sh":   hdir + "/.bash_history",
	}
}

var histformatter = map[string]func(string) string{
	"zsh": func(s string) string {
		return fmt.Sprintf(": %d:0;%s\n", time.Now().Unix(), s)
	},
	"bash": func(s string) string {
		return s + "\n"
	},
	"sh": func(s string) string {
		return s + "\n"
	},
}

func RecordHistory(cmd string) error {
	var err error

	bin := filepath.Base(os.Args[0])
	cmd = bin + " " + cmd

	sh := filepath.Base(os.Getenv("SHELL"))

	histfile, ok := dfhistfile[sh]
	if !ok {
		return err
	}

	formatter, ok := histformatter[sh]
	if !ok {
		return err
	}

	histfile, err = filepath.Abs(histfile)
	if err != nil {
		l.Errorf("abs file fail :%s", err)
		return err
	}

	f, err := os.OpenFile(histfile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		l.Errorf("open file fail : %s", err)
		return err
	}

	_, err = f.WriteString(formatter(cmd))
	if err != nil {
		l.Errorf("write cmd fail : %s", err)
		return err
	}

	return nil
}
