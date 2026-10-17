package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/net/webdav"
)

// uploadKey carries an upload to the file system of the vault.
type uploadKey struct{}

// upload is the request body of an upload: its declared size, and whether reading it failed
// (the connection was cut), which the file system cannot see by itself.
type upload struct {
	body io.ReadCloser
	size int64
	cut  bool
}

func (u *upload) Read(p []byte) (int, error) {
	n, err := u.body.Read(p)
	if err != nil && err != io.EOF {
		u.cut = true
	}
	return n, err
}

func (u *upload) Close() error {
	return u.body.Close()
}

// withUpload makes the body of an upload seen by the file system of the vault.
func withUpload(r *http.Request) *http.Request {
	body := &upload{body: r.Body, size: r.ContentLength}
	r = r.WithContext(context.WithValue(r.Context(), uploadKey{}, body))
	r.Body = body
	return r
}

// atomicDir is the folder of the vault, where an upload is written to a temporary file that
// takes the place of the save only once it arrived whole: a connection cut in the middle (the
// console going to sleep, the Wi-Fi) leaves the previous save instead of a cut one.
type atomicDir struct {
	webdav.Dir
}

func (d atomicDir) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	body, ok := ctx.Value(uploadKey{}).(*upload)
	if !ok || flag&os.O_CREATE == 0 || flag&(os.O_WRONLY|os.O_RDWR) == 0 {
		return d.Dir.OpenFile(ctx, name, flag, perm)
	}
	// the same names as webdav.Dir accepts
	if filepath.Separator != '/' && strings.ContainsRune(name, filepath.Separator) {
		return nil, os.ErrNotExist
	}
	target := filepath.Join(string(d.Dir), filepath.FromSlash(path.Clean("/"+name)))
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return nil, os.ErrExist
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*.part")
	if err != nil {
		return nil, err
	}
	return &uploadFile{File: temporary, target: target, body: body}, nil
}

// uploadFile is an upload on its way to the vault.
type uploadFile struct {
	*os.File
	target  string
	body    *upload
	written int64
	failed  bool
}

func (f *uploadFile) Write(p []byte) (int, error) {
	n, err := f.File.Write(p)
	f.written += int64(n)
	if err != nil {
		f.failed = true
	}
	return n, err
}

// ReadFrom goes through Write: the one of the file would not count what is written.
func (f *uploadFile) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{f}, r)
}

// Close puts the upload in place when it arrived whole, and forgets it otherwise.
func (f *uploadFile) Close() error {
	name := f.File.Name()
	err := f.File.Sync()
	if closeErr := f.File.Close(); err == nil {
		err = closeErr
	}
	if err == nil && (f.failed || f.body.cut || (f.body.size >= 0 && f.written != f.body.size)) {
		err = errUploadIncomplete
	}
	if err == nil {
		err = os.Rename(name, f.target)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}

var errUploadIncomplete = errors.New("the upload did not arrive whole")
