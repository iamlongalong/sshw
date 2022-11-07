package sshw

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScp(t *testing.T) {
	p := "scp.go"
	info, err := os.Stat(p)
	assert.Nil(t, err)

	assert.False(t, info.IsDir())

	f, _ := os.Open(p)
	defer f.Close()

	s := fmt.Sprintf("%04d", info.Mode())
	fmt.Println(s)
}

func TestBase(t *testing.T) {
	ps := []string{
		"./",
		"./xx",
		"/",
		"/xx/",
		"~",
		"~/xx",
		"~/.",
		"/.",
		"/./",
		"/../.",
		"/xx",
		"/.././xx",
	}

	bs := []string{}

	for _, p := range ps {
		bs = append(bs, filepath.Base(filepath.Clean(p)))
	}

	fmt.Printf("%+v\n", bs)
}