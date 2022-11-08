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

func CopyFromRemote(ctx context.Context, s *ssh.Session, remotePath string, localPath string) error {
	f, err := os.OpenFile(localPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return errors.Wrap(err, "open file fail")
	}

	wg := sync.WaitGroup{}
	errCh := make(chan error, 1)

	wg.Add(1)
	go func() {
		var err error

		defer func() {
			// We must unblock the go routine first as we block on reading the channel later
			wg.Done()

			errCh <- err
		}()

		r, err := s.StdoutPipe()
		if err != nil {
			errCh <- err
			return
		}

		in, err := s.StdinPipe()
		if err != nil {
			errCh <- err
			return
		}
		defer in.Close()

		err = s.Start(fmt.Sprintf("scp -f %q", remotePath))
		if err != nil {
			errCh <- err
			return
		}

		err = Ack(in)
		if err != nil {
			errCh <- err
			return
		}

		res, err := ParseResponse(r)
		if err != nil {
			errCh <- err
			return
		}
		if res.IsFailure() {
			errCh <- errors.New(res.GetMessage())
			return
		}

		infos, err := res.ParseFileInfos()
		if err != nil {
			errCh <- err
			return
		}

		err = Ack(in)
		if err != nil {
			errCh <- err
			return
		}

		bar := getBar(infos.Size, "downloading : "+infos.Filename)

		_, err = CopyN(io.MultiWriter(f, bar), r, infos.Size)
		if err != nil {
			errCh <- err
			return
		}

		err = Ack(in)
		if err != nil {
			errCh <- err
			return
		}

		err = s.Wait()
		if err != nil {
			errCh <- err
			return
		}
	}()

	if err := wait(ctx, &wg); err != nil {
		return err
	}
	finalErr := <-errCh
	close(errCh)

	return finalErr
}

func CopyFromLocal(ctx context.Context, s *ssh.Session, localPath string, remotePath string) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return errors.Wrap(err, "get file fail")
	}

	if info.IsDir() {
		return errors.Errorf("can not send a dir : %s", localPath)
	}

	f, _ := os.Open(localPath)
	defer f.Close()

	stdout, err := s.StdoutPipe()
	if err != nil {
		return errors.Wrap(err, "get stdout pipe fail")
	}

	wg := sync.WaitGroup{}
	wg.Add(2)

	errCh := make(chan error, 2)

	go func() {
		defer wg.Done()
		w, err := s.StdinPipe()
		if err != nil {
			errCh <- errors.Wrap(err, "get stdin fail")
			return
		}

		defer w.Close()

		_, err = fmt.Fprintln(w, "C0644", info.Size(), filepath.Base(remotePath))
		if err != nil {
			errCh <- errors.Wrap(err, "write command fail")
			return
		}

		if err = checkResponse(stdout); err != nil {
			errCh <- errors.Wrap(err, "check response 1 fail")
			return
		}

		bar := getBar(info.Size(), "uploading : "+info.Name())

		_, err = io.CopyN(io.MultiWriter(w, bar), f, info.Size())
		if err != nil {
			errCh <- errors.Wrap(err, "copy fail")
			return
		}

		_, err = fmt.Fprint(w, "\x00")
		if err != nil {
			errCh <- errors.Wrap(err, "write fail")
			return
		}

		if err = checkResponse(stdout); err != nil {
			errCh <- errors.Wrap(err, "check response 2 fail")
			return
		}
	}()

	go func() {
		defer wg.Done()
		cmd := fmt.Sprintf("scp -vt %q", remotePath)
		err := s.Run(cmd)
		if err != nil {
			errCh <- errors.Wrap(err, "run scp fail")
			return
		}
	}()

	if err := wait(ctx, &wg); err != nil {
		return errors.Wrap(err, "wait fail")
	}

	close(errCh)
	for err := range errCh {
		if err != nil {
			return errors.Wrap(err, "copy from local fail")
		}
	}

	return nil
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
