package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"sshw"

	"github.com/manifoldco/promptui"
)

const prev = "-parent-"

var (
	Build = "devel"
	V     = flag.Bool("version", false, "show version")
	H     = flag.Bool("help", false, "show help")
	S     = flag.Bool("s", false, "use local ssh config '~/.ssh/config'")

	log = sshw.GetLogger()

	templates = &promptui.SelectTemplates{
		Label:    "✨ {{ . | green}}",
		Active:   "➤ {{ .Name | cyan  }}{{if .Alias}}({{.Alias | yellow}}){{end}} {{if .Host}}{{if .User}}{{.User | faint}}{{`@` | faint}}{{end}}{{.Host | faint}}{{end}}",
		Inactive: "  {{.Name | faint}}{{if .Alias}}({{.Alias | faint}}){{end}} {{if .Host}}{{if .User}}{{.User | faint}}{{`@` | faint}}{{end}}{{.Host | faint}}{{end}}",
	}
)

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

func findAlias(nodes []*sshw.Node, nodeAlias string) *sshw.Node {
	for _, node := range nodes {
		if node.Alias == nodeAlias {
			return node
		}
		if len(node.Children) > 0 {
			return findAlias(node.Children, nodeAlias)
		}
	}
	return nil
}

func main() {
	flag.Parse()
	if !flag.Parsed() {
		flag.Usage()
		return
	}

	if *H {
		flag.Usage()
		return
	}

	if *V {
		fmt.Println("sshw - ssh client wrapper for automatic login")
		fmt.Println("  git version:", Build)
		fmt.Println("  go version :", runtime.Version())
		return
	}
	if *S {
		err := sshw.LoadSshConfig()
		if err != nil {
			log.Error("load ssh config error", err)
			os.Exit(1)
		}
	} else {
		err := sshw.LoadConfig()
		if err != nil {
			log.Error("load config error", err)
			os.Exit(1)
		}
	}

	var nodes = sshw.GetConfig()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "scp":
			base := strings.Join(os.Args[1:len(os.Args)], " ")

			node := choose(nil, sshw.GetConfig())
			// var node = findName(nodes, "ssdev")
			if node == nil {
				return
			}

			msg := base + " " + node.Host + ":"

			if node.User != "" {
				msg = base + " " + node.User + "@" + node.Host + ":"
			}

			fmt.Print(msg)

			reader := bufio.NewReader(os.Stdin)
			strBytes, _, _ := reader.ReadLine()
			after := string(strBytes)

			str := base + " " + node.Host + ":" + after
			// opt, err := sshw.ParseScpOption(base)
			opt, err := sshw.ParseScpOption(str)
			if err != nil {
				// fmt.Println(str)
				// fmt.Printf("%+v", opt)

				log.Error(err)
				os.Exit(1)
				return
			}

			client := sshw.NewClient(node)
			client.Scp(opt)
			return
		default: // login by alias
			var nodeAlias = os.Args[1]
			var node = findAlias(nodes, nodeAlias)
			if node != nil {
				client := sshw.NewClient(node)
				client.Login()
				return
			}
		}
	}

	node := choose(nil, sshw.GetConfig())
	if node == nil {
		return
	}

	client := sshw.NewClient(node)
	client.Login()
}

func choose(parent, trees []*sshw.Node) *sshw.Node {
	prompt := promptui.Select{
		Label:        "select host",
		Items:        trees,
		Templates:    templates,
		Size:         20,
		HideSelected: true,
		Searcher: func(input string, index int) bool {
			node := trees[index]
			content := fmt.Sprintf("%s %s %s", node.Name, node.User, node.Host)
			if strings.Contains(input, " ") {
				for _, key := range strings.Split(input, " ") {
					key = strings.TrimSpace(key)
					if key != "" {
						if !strings.Contains(content, key) {
							return false
						}
					}
				}
				return true
			}
			if strings.Contains(content, input) {
				return true
			}
			return false
		},
	}
	index, _, err := prompt.Run()
	if err != nil {
		return nil
	}

	node := trees[index]
	if len(node.Children) > 0 {
		first := node.Children[0]
		if first.Name != prev {
			first = &sshw.Node{Name: prev}
			node.Children = append(node.Children[:0], append([]*sshw.Node{first}, node.Children...)...)
		}
		return choose(trees, node.Children)
	}

	if node.Name == prev {
		if parent == nil {
			return choose(nil, sshw.GetConfig())
		}
		return choose(nil, parent)
	}

	return node
}
