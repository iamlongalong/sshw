package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/iamlongalong/sshw"
	"github.com/pkg/errors"
)

var ErrHostNotFound = errors.New("host not found")

type Commander interface {
	Execute() error
}

type SCPCommand struct {
	args []string
	log  sshw.Logger
}

type LoginCommand struct {
	nodeAlias string
	log       sshw.Logger
}

func NewSCPCommand(args []string, log sshw.Logger) *SCPCommand {
	return &SCPCommand{args: args, log: log}
}

func NewLoginCommand(nodeAlias string, log sshw.Logger) *LoginCommand {
	return &LoginCommand{nodeAlias: nodeAlias, log: log}
}

func (c *SCPCommand) Execute() error {
	base := strings.Join(c.args, " ")
	cmd := ""
	cmdReady := false
	shouldRecordHistory := false

	if len(c.args) == 2 { // sshw scp xxxx
		src := c.args[1]
		h, _, _ := sshw.ParseHostFile(src)
		if h != "" {
			cmd = base + " ./" // sshw scp xx:/tmp/x.txt  =>  sshw scp xx:/tmp/x.txt ./
			cmdReady = true
		}
	}

	if len(c.args) >= 3 {
		cmd = base
		cmdReady = true
	}

	if !cmdReady {
		shouldRecordHistory = true
		node := choose(nil, sshw.GetConfig())
		if node == nil {
			return errors.New("no node selected")
		}

		msg := base + " " + node.Host + ":"

		if node.User != "" {
			msg = base + " " + node.User + "@" + node.Host + ":"
		}

		fmt.Print(msg)

		reader := bufio.NewReader(os.Stdin)
		strBytes, _, _ := reader.ReadLine()
		after := string(strBytes)

		cmd = base + " " + node.Host + ":" + after
	}

	opt, err := sshw.ParseScpOption(cmd)
	if err != nil {
		return errors.Wrap(err, "failed to parse scp command")
	}

	var node *sshw.Node
	if opt.SrcHost != "" {
		node = findNameOrAliasOrHost(sshw.GetConfig(), opt.SrcHost)
		if node == nil {
			return errors.Wrapf(ErrHostNotFound, "source host not found: %s", opt.SrcHost)
		}
	} else {
		node = findNameOrAliasOrHost(sshw.GetConfig(), opt.TarHost)
		if node == nil {
			return errors.Wrapf(ErrHostNotFound, "target host not found: %s", opt.TarHost)
		}
	}

	client := sshw.NewClient(node)
	client.Scp(opt)

	if shouldRecordHistory {
		sshw.RecordHistory(cmd)
	}

	return nil
}

func (c *LoginCommand) Execute() error {
	if c.nodeAlias == "" {
		node := choose(nil, sshw.GetConfig())
		if node == nil {
			return errors.New("no node selected")
		}
		client := sshw.NewClient(node)
		sshw.RecordHistory(node.Host)
		return client.Login()
	}

	node := findNameOrAliasOrHost(sshw.GetConfig(), c.nodeAlias)
	if node == nil {
		return errors.Wrapf(ErrHostNotFound, "host not found: %s", c.nodeAlias)
	}

	client := sshw.NewClient(node)
	return client.Login()
}

// Helper functions
func findNameOrAliasOrHost(nodes []*sshw.Node, nameOrAliasOrHost string) *sshw.Node {
	node := findName(nodes, nameOrAliasOrHost)
	if node != nil {
		return node
	}

	node = findAlias(nodes, nameOrAliasOrHost)
	if node != nil {
		return node
	}

	return findHost(nodes, nameOrAliasOrHost)
}

func findName(nodes []*sshw.Node, name string) *sshw.Node {
	for _, node := range nodes {
		if node.Name == name {
			return node
		}
		if len(node.Children) > 0 {
			n := findName(node.Children, name)
			if n != nil {
				return n
			}
		}
	}
	return nil
}

func findHost(nodes []*sshw.Node, nodeHost string) *sshw.Node {
	for _, node := range nodes {
		if node.Host == nodeHost {
			return node
		}
		if len(node.Children) > 0 {
			node = findHost(node.Children, nodeHost)
			if node != nil {
				return node
			}
		}
	}
	return nil
}

func findAlias(nodes []*sshw.Node, nodeAlias string) *sshw.Node {
	for _, node := range nodes {
		if node.Alias == nodeAlias {
			return node
		}
		if len(node.Children) > 0 {
			node = findAlias(node.Children, nodeAlias)
			if node != nil {
				return node
			}
		}
	}
	return nil
}
