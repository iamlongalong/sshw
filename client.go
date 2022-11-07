package sshw

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/terminal"
)

var (
	DefaultCiphers = []string{
		"aes128-ctr",
		"aes192-ctr",
		"aes256-ctr",
		"aes128-gcm@openssh.com",
		"chacha20-poly1305@openssh.com",
		"arcfour256",
		"arcfour128",
		"arcfour",
		"aes128-cbc",
		"3des-cbc",
		"blowfish-cbc",
		"cast128-cbc",
		"aes192-cbc",
		"aes256-cbc",
	}
)

type Client interface {
	Login()
	Scp(ScpOption)
}

type defaultClient struct {
	clientConfig *ssh.ClientConfig
	node         *Node
	client       *ssh.Client
}

func genSSHConfig(node *Node) *defaultClient {
	u, err := user.Current()
	if err != nil {
		l.Error(err)
		return nil
	}

	var authMethods []ssh.AuthMethod

	var pemBytes []byte
	if node.KeyPath == "" {
		pemBytes, err = ioutil.ReadFile(path.Join(u.HomeDir, ".ssh/id_rsa"))
	} else {
		pemBytes, err = ioutil.ReadFile(node.KeyPath)
	}
	if err != nil {
		l.Error(err)
	} else {
		var signer ssh.Signer
		if node.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(pemBytes, []byte(node.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(pemBytes)
		}
		if err != nil {
			l.Error(err)
		} else {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
		}
	}

	password := node.password()

	if password != nil {
		authMethods = append(authMethods, password)
	}

	authMethods = append(authMethods, ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
		answers := make([]string, 0, len(questions))
		for i, q := range questions {
			fmt.Print(q)
			if echos[i] {
				scan := bufio.NewScanner(os.Stdin)
				if scan.Scan() {
					answers = append(answers, scan.Text())
				}
				err := scan.Err()
				if err != nil {
					return nil, err
				}
			} else {
				b, err := terminal.ReadPassword(int(syscall.Stdin))
				if err != nil {
					return nil, err
				}
				fmt.Println()
				answers = append(answers, string(b))
			}
		}
		return answers, nil
	}))

	config := &ssh.ClientConfig{
		User:            node.user(),
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         time.Second * 10,
	}

	config.SetDefaults()
	config.Ciphers = append(config.Ciphers, DefaultCiphers...)

	return &defaultClient{
		clientConfig: config,
		node:         node,
	}
}

func NewClient(node *Node) Client {
	return genSSHConfig(node)
}

func (c *defaultClient) Scp(opt ScpOption) {
	err := c.connect()
	if err != nil {
		l.Error(err)
	}
	defer c.Close()

	err = opt.Valid()
	if err != nil {
		l.Error(err)
		os.Exit(1)
		return
	}

	session, err := c.client.NewSession()
	if err != nil {
		l.Error(err)
		os.Exit(1)
		return
	}

	if opt.srcHost == "" {
		err = CopyFromLocal(context.Background(), session, opt.srcFilePath, opt.tarFilePath)
	} else {
		err = CopyFromRemote(context.Background(), session, opt.srcFilePath, opt.tarFilePath)
	}

	if err != nil {
		l.Error(err)
		os.Exit(1)
		return
	}
}

func (c *defaultClient) Login() {
	c.connect()
	defer c.Close()

	session, err := c.client.NewSession()
	if err != nil {
		l.Error(err)
		os.Exit(1)
		return
	}

	fd := int(os.Stdin.Fd())
	state, err := terminal.MakeRaw(fd)
	if err != nil {
		l.Error(err)
		return
	}
	defer terminal.Restore(fd, state)

	w, h, err := terminal.GetSize(fd)
	if err != nil {
		l.Error(err)
		return
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	err = session.RequestPty("xterm", h, w, modes)
	if err != nil {
		l.Error(err)
		return
	}

	session.Stdout = os.Stdout
	session.Stderr = os.Stderr
	stdinPipe, err := session.StdinPipe()
	if err != nil {
		l.Error(err)
		return
	}

	err = session.Shell()
	if err != nil {
		l.Error(err)
		return
	}

	// then callback
	for i := range c.node.CallbackShells {
		shell := c.node.CallbackShells[i]
		time.Sleep(shell.Delay * time.Millisecond)
		stdinPipe.Write([]byte(shell.Cmd + "\r"))
	}

	// change stdin to user
	go func() {
		_, err = io.Copy(stdinPipe, os.Stdin)
		l.Error(err)
		session.Close()
	}()

	// interval get terminal size
	// fix resize issue
	go func() {
		var (
			ow = w
			oh = h
		)
		for {
			cw, ch, err := terminal.GetSize(fd)
			if err != nil {
				break
			}

			if cw != ow || ch != oh {
				err = session.WindowChange(ch, cw)
				if err != nil {
					break
				}
				ow = cw
				oh = ch
			}
			time.Sleep(time.Second)
		}
	}()

	// send keepalive
	go func() {
		for {
			time.Sleep(time.Second * 10)
			c.client.SendRequest("keepalive@openssh.com", false, nil)
		}
	}()

	session.Wait()
}

func (c *defaultClient) Close() error {
	return c.client.Close()
}

func (c *defaultClient) connect() error {
	host := c.node.Host
	port := strconv.Itoa(c.node.port())
	jNodes := c.node.Jump

	var client *ssh.Client

	if len(jNodes) > 0 {
		jNode := jNodes[0]
		jc := genSSHConfig(jNode)
		proxyClient, err := ssh.Dial("tcp", net.JoinHostPort(jNode.Host, strconv.Itoa(jNode.port())), jc.clientConfig)
		if err != nil {
			l.Error(err)
			return err
		}
		conn, err := proxyClient.Dial("tcp", net.JoinHostPort(host, port))
		if err != nil {
			l.Error(err)
			return err
		}
		ncc, chans, reqs, err := ssh.NewClientConn(conn, net.JoinHostPort(host, port), c.clientConfig)
		if err != nil {
			l.Error(err)
			return err
		}
		client = ssh.NewClient(ncc, chans, reqs)
	} else {
		client1, err := ssh.Dial("tcp", net.JoinHostPort(host, port), c.clientConfig)
		client = client1
		if err != nil {
			msg := err.Error()
			// use terminal password retry
			if strings.Contains(msg, "no supported methods remain") && !strings.Contains(msg, "password") {
				fmt.Printf("%s@%s's password:", c.clientConfig.User, host)
				var b []byte
				b, err = terminal.ReadPassword(int(syscall.Stdin))
				if err == nil {
					p := string(b)
					if p != "" {
						c.clientConfig.Auth = append(c.clientConfig.Auth, ssh.Password(p))
					}
					client, err = ssh.Dial("tcp", net.JoinHostPort(host, port), c.clientConfig)
				}
			}
		}
		if err != nil {
			l.Error(err)
			return err
		}
	}

	l.Infof("connect server ssh -p %d %s@%s version: %s\n", c.node.port(), c.node.user(), host, string(client.ServerVersion()))

	c.client = client

	return nil
}

type ScpOption struct {
	srcFilePath string
	srcHost     string

	tarFilePath string
	tarHost     string
}

func (o *ScpOption) Valid() error {
	if o.srcHost == "" && o.tarHost == "" {
		return errors.New("src host and tar host can not be empty both")
	}

	if o.srcHost != "" && o.tarHost != "" {
		return errors.New("src host and tar host can not be remote host both")
	}

	if strings.HasSuffix(o.srcFilePath, "/") {
		return errors.New("do not support dir yet")
	}

	if strings.HasSuffix(o.tarFilePath, "/") {
		_, fn := filepath.Split(o.srcFilePath)
		o.tarFilePath = o.tarFilePath + fn
	}

	return nil
}

func ParseScpOption(s string) (ScpOption, error) {
	ss := strings.Split(s, " ")

	sstar := make([]string, 0)
	for _, ssitem := range ss {
		if strings.TrimSpace(ssitem) != "" {
			sstar = append(sstar, strings.TrimSpace(ssitem))
		}
	}

	if len(sstar) != 3 {
		return ScpOption{}, errors.Errorf("fail to parse scp syntax : %s", s)
	}

	if sstar[0] != "scp" {
		return ScpOption{}, errors.Errorf("fail to parse scp syntax : %s", s)
	}

	var err error
	opt := ScpOption{}

	srcStr := sstar[1]

	opt.srcHost, opt.srcFilePath, err = parseHostFile(srcStr)
	if err != nil {
		return ScpOption{}, err
	}

	tarStr := sstar[2]
	opt.tarHost, opt.tarFilePath, err = parseHostFile(tarStr)
	if err != nil {
		return ScpOption{}, err
	}

	return opt, opt.Valid()
}

func parseHostFile(s string) (host string, filePath string, err error) {
	ss := strings.Split(s, ":")
	if len(ss) == 2 {
		return ss[0], ss[1], nil
	}

	if len(ss) == 1 {
		return "", ss[0], nil
	}

	return "", "", errors.Errorf("parse host fail : %s", s)
}
