package sshw

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/pkg/errors"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/crypto/ssh"
)

// ProgressWriter 定义进度显示接口
type ProgressWriter interface {
	io.Writer
	Close() error
}

// FileTransferError 统一的文件传输错误类型
type FileTransferError struct {
	Op  string
	Err error
}

func (e *FileTransferError) Error() string {
	return fmt.Sprintf("%s: %v", e.Op, e.Err)
}

func wrapError(op string, err error) error {
	return &FileTransferError{Op: op, Err: err}
}

func CopyFromRemote(ctx context.Context, s *ssh.Session, remotePath string, localPath string) error {
	f, err := os.OpenFile(localPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return wrapError("open local file", err)
	}
	defer f.Close()

	done := make(chan error, 1)
	go func() {
		var err error
		defer func() {
			done <- err
		}()

		r, err := s.StdoutPipe()
		if err != nil {
			err = wrapError("get stdout pipe", err)
			return
		}

		in, err := s.StdinPipe()
		if err != nil {
			err = wrapError("get stdin pipe", err)
			return
		}
		defer in.Close()

		if err = s.Start(fmt.Sprintf("scp -f %q", remotePath)); err != nil {
			err = wrapError("start scp command", err)
			return
		}

		if err = Ack(in); err != nil {
			err = wrapError("send initial ack", err)
			return
		}

		res, err := ParseResponse(r)
		if err != nil {
			err = wrapError("parse response", err)
			return
		}
		if res.IsFailure() {
			err = wrapError("remote error", errors.New(res.GetMessage()))
			return
		}

		infos, err := res.ParseFileInfos()
		if err != nil {
			err = wrapError("parse file info", err)
			return
		}

		if err = Ack(in); err != nil {
			err = wrapError("send file info ack", err)
			return
		}

		bar := getBar(infos.Size, "downloading : "+infos.Filename)
		defer bar.(ProgressWriter).Close()

		if _, err = CopyN(io.MultiWriter(f, bar), r, infos.Size); err != nil {
			err = wrapError("copy file content", err)
			return
		}

		if err = Ack(in); err != nil {
			err = wrapError("send final ack", err)
			return
		}

		if err = s.Wait(); err != nil {
			err = wrapError("wait for scp completion", err)
			return
		}
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return wrapError("context cancelled", ctx.Err())
	}
}

func CopyFromLocal(ctx context.Context, s *ssh.Session, localPath string, remotePath string) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return wrapError("get file info", err)
	}

	if info.IsDir() {
		return wrapError("validate file type", errors.New("cannot send a directory"))
	}

	f, err := os.Open(localPath)
	if err != nil {
		return wrapError("open local file", err)
	}
	defer f.Close()

	stdout, err := s.StdoutPipe()
	if err != nil {
		return wrapError("get stdout pipe", err)
	}

	done := make(chan error, 1)
	go func() {
		var err error
		defer func() {
			done <- err
		}()

		w, err := s.StdinPipe()
		if err != nil {
			err = wrapError("get stdin pipe", err)
			return
		}
		defer w.Close()

		_, err = fmt.Fprintln(w, "C0644", info.Size(), filepath.Base(remotePath))
		if err != nil {
			err = wrapError("write file info", err)
			return
		}

		if err = checkResponse(stdout); err != nil {
			err = wrapError("check initial response", err)
			return
		}

		bar := getBar(info.Size(), "uploading : "+info.Name())
		defer bar.(ProgressWriter).Close()

		_, err = io.CopyN(io.MultiWriter(w, bar), f, info.Size())
		if err != nil {
			err = wrapError("copy file content", err)
			return
		}

		_, err = fmt.Fprint(w, "\x00")
		if err != nil {
			err = wrapError("write end marker", err)
			return
		}

		if err = checkResponse(stdout); err != nil {
			err = wrapError("check final response", err)
			return
		}

		cmd := fmt.Sprintf("scp -vt %q", remotePath)
		if err = s.Run(cmd); err != nil {
			err = wrapError("run scp command", err)
			return
		}
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return wrapError("context cancelled", ctx.Err())
	}
}

func checkResponse(r io.Reader) error {
	response, err := ParseResponse(r)
	if err != nil {
		return err
	}

	if response.IsFailure() {
		return errors.New(response.GetMessage())
	}

	return nil

}

func wait(ctx context.Context, wg *sync.WaitGroup) error {
	c := make(chan struct{})
	go func() {
		defer close(c)
		wg.Wait()
	}()

	select {
	case <-c:
		return nil

	case <-ctx.Done():
		return ctx.Err()
	}
}

func CopyN(writer io.Writer, src io.Reader, size int64) (int64, error) {
	var total int64
	total = 0
	for total < size {
		n, err := io.CopyN(writer, src, size)
		if err != nil {
			return 0, err
		}
		total += n
	}

	return total, nil
}

func getBar(size int64, desc string) io.Writer {
	bar := progressbar.NewOptions(int(size),
		// progressbar.OptionSetWriter(ansi.NewAnsiStdout()),
		progressbar.OptionEnableColorCodes(true),

		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(25),
		progressbar.OptionSetDescription(desc),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}))

	return bar
}
