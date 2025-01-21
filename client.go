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
	"golang.org/x/term"
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
	Login() error
	Scp(ScpOption) error
}

type defaultClient struct {
	clientConfig *ssh.ClientConfig
	node         *Node
	client       *ssh.Client
}

func genSSHConfig(node *Node) *defaultClient {
	u, err := user.Current()
	if err != nil {
		return nil
	}

	authMethods, err := buildAuthMethods(node, u)
	if err != nil {
		l.Error(err)
	}

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

func buildAuthMethods(node *Node, u *user.User) ([]ssh.AuthMethod, error) {
	var authMethods []ssh.AuthMethod

	// Add public key authentication
	if err := addPublicKeyAuth(&authMethods, node, u); err != nil {
		l.Error(err)
	}

	// Add password authentication if available
	if password := node.password(); password != nil {
		authMethods = append(authMethods, password)
	}

	// Add keyboard interactive authentication
	authMethods = append(authMethods, keyboardInteractiveAuth())

	return authMethods, nil
}

func addPublicKeyAuth(authMethods *[]ssh.AuthMethod, node *Node, u *user.User) error {
	var keyPath string
	if node.KeyPath == "" {
		keyPath = path.Join(u.HomeDir, ".ssh/id_rsa")
	} else {
		keyPath = node.KeyPath
	}

	pemBytes, err := ioutil.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("failed to read key file: %w", err)
	}

	var signer ssh.Signer
	if node.Passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(pemBytes, []byte(node.Passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey(pemBytes)
	}

	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}

	*authMethods = append(*authMethods, ssh.PublicKeys(signer))
	return nil
}

func keyboardInteractiveAuth() ssh.AuthMethod {
	return ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
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
				b, err := term.ReadPassword(int(syscall.Stdin))
				if err != nil {
					return nil, err
				}
				fmt.Println()
				answers = append(answers, string(b))
			}
		}
		return answers, nil
	})
}

func NewClient(node *Node) Client {
	return genSSHConfig(node)
}

type SessionManager struct {
	session   *ssh.Session
	stdinPipe io.WriteCloser
	fd        int
	state     *term.State
}

func NewSessionManager(session *ssh.Session) (*SessionManager, error) {
	fd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("failed to make terminal raw: %w", err)
	}

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		term.Restore(fd, state)
		return nil, fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	return &SessionManager{
		session:   session,
		stdinPipe: stdinPipe,
		fd:        fd,
		state:     state,
	}, nil
}

func (sm *SessionManager) Close() {
	if sm.state != nil {
		term.Restore(sm.fd, sm.state)
	}
	if sm.session != nil {
		sm.session.Close()
	}
}

func (c *defaultClient) Scp(opt ScpOption) error {
	err := c.connect()
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer c.Close()

	err = opt.Valid()
	if err != nil {
		return fmt.Errorf("invalid scp option: %w", err)
	}

	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	if opt.SrcHost == "" {
		err = CopyFromLocal(context.Background(), session, opt.SrcFilePath, opt.TarFilePath)
	} else {
		err = CopyFromRemote(context.Background(), session, opt.SrcFilePath, opt.TarFilePath)
	}

	if err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	fmt.Println("")
	fmt.Println("✅  copy file success")
	fmt.Println("")
	return nil
}

func (c *defaultClient) Login() error {
	err := c.connect()
	if err != nil {
		return err
	}
	defer c.Close()

	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	sm, err := NewSessionManager(session)
	if err != nil {
		return fmt.Errorf("failed to create session manager: %w", err)
	}
	defer sm.Close()

	w, h, err := term.GetSize(sm.fd)
	if err != nil {
		return fmt.Errorf("failed to get terminal size: %w", err)
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	err = session.RequestPty("xterm", h, w, modes)
	if err != nil {
		return fmt.Errorf("failed to request pty: %w", err)
	}

	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	err = session.Shell()
	if err != nil {
		return fmt.Errorf("failed to start shell: %w", err)
	}

	// Execute callback shells
	for _, shell := range c.node.CallbackShells {
		time.Sleep(shell.Delay * time.Millisecond)
		_, err = sm.stdinPipe.Write([]byte(shell.Cmd + "\r"))
		if err != nil {
			return fmt.Errorf("failed to write callback shell: %w", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle user input
	go func() {
		_, err = io.Copy(sm.stdinPipe, os.Stdin)
		if err != nil {
			l.Error(fmt.Errorf("stdin copy error: %w", err))
		}
		cancel()
	}()

	// Handle terminal resize
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		ow, oh := w, h
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cw, ch, err := term.GetSize(sm.fd)
				if err != nil {
					l.Error(fmt.Errorf("get terminal size error: %w", err))
					return
				}

				if cw != ow || ch != oh {
					err = session.WindowChange(ch, cw)
					if err != nil {
						l.Error(fmt.Errorf("window change error: %w", err))
						return
					}
					ow, oh = cw, ch
				}
			}
		}
	}()

	// Handle keepalive
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _, err := c.client.SendRequest("keepalive@openssh.com", false, nil)
				if err != nil {
					l.Error(fmt.Errorf("keepalive error: %w", err))
					return
				}
			}
		}
	}()

	return session.Wait()
}

func (c *defaultClient) Close() error {
	return c.client.Close()
}

func (c *defaultClient) connect() error {
	host := c.node.Host
	port := strconv.Itoa(c.node.port())
	jNodes := c.node.Jump

	var client *ssh.Client
	var err error

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
		client, err = ssh.Dial("tcp", net.JoinHostPort(host, port), c.clientConfig)
		if err != nil {
			msg := err.Error()
			// use terminal password retry
			if strings.Contains(msg, "no supported methods remain") && !strings.Contains(msg, "password") {
				fmt.Printf("%s@%s's password:", c.clientConfig.User, host)
				var b []byte
				b, err = term.ReadPassword(int(syscall.Stdin))
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

	// l.Infof("connect server ssh -p %d %s@%s version: %s\n", c.node.port(), c.node.user(), host, string(client.ServerVersion()))

	c.client = client

	return nil
}

type ScpOption struct {
	SrcFilePath string
	SrcHost     string

	TarFilePath string
	TarHost     string
}

func (o *ScpOption) Valid() error {
	if o.SrcHost == "" && o.TarHost == "" {
		return errors.New("src host and tar host can not be empty both")
	}

	if o.SrcHost != "" && o.TarHost != "" {
		return errors.New("src host and tar host can not be remote host both")
	}

	if o.SrcFilePath == "" || o.TarFilePath == "" {
		return errors.New("src filepath or tar filepath should not be empty")
	}

	// check to ban dir copy
	srcbase := filepath.Base(o.SrcFilePath)
	if strings.HasSuffix(srcbase, "/") || strings.HasSuffix(srcbase, ".") || strings.HasSuffix(srcbase, "~") {
		return errors.New("do not support dir yet")
	}

	// convert for copy to dir
	tarbase := filepath.Base(o.TarFilePath)
	if strings.HasSuffix(tarbase, "/") || strings.HasSuffix(tarbase, ".") || strings.HasSuffix(tarbase, "~") {
		o.TarFilePath = filepath.Join(filepath.Clean(o.TarFilePath), filepath.Base(o.SrcFilePath))
	}

	// change ~ that scp may not support
	if o.SrcHost != "" && strings.HasPrefix(o.SrcFilePath, "~") {
		o.SrcFilePath = "." + o.SrcFilePath[1:]
	}

	// change ~ that scp may not support
	if o.TarHost != "" && strings.HasPrefix(o.TarFilePath, "~") {
		o.TarFilePath = "." + o.TarFilePath[1:]
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

	if len(sstar) != 3 { // 默认 target 地址为 ./
		sstar = append(sstar, "./")
	}

	if sstar[0] != "scp" {
		return ScpOption{}, errors.Errorf("fail to parse scp syntax : %s", s)
	}

	var err error
	opt := ScpOption{}

	srcStr := sstar[1]

	opt.SrcHost, opt.SrcFilePath, err = ParseHostFile(srcStr)
	if err != nil {
		return ScpOption{}, err
	}

	tarStr := sstar[2]
	opt.TarHost, opt.TarFilePath, err = ParseHostFile(tarStr)
	if err != nil {
		return ScpOption{}, err
	}

	return opt, opt.Valid()
}

func ParseHostFile(s string) (host string, filePath string, err error) {
	ss := strings.Split(s, ":")
	if len(ss) == 2 {
		return ss[0], parseRelativePath(ss[1]), nil
	}

	if len(ss) == 1 {
		return "", parseRelativePath(ss[0]), nil
	}

	return "", "", errors.Errorf("parse host fail : %s", s)
}

func parseRelativePath(s string) string {
	if s == "" || s == "." || s == "./" {
		return "./"
	}

	cs := filepath.Clean(s)
	if filepath.HasPrefix(cs, "./") || filepath.HasPrefix(cs, "/") || filepath.HasPrefix(cs, "~") {
		return cs
	}

	return "./" + cs
}
