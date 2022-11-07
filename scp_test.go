package sshw

import (
	"fmt"
	"os"
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
