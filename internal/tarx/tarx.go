// Package tarx packs host files and directories into tar streams for
// providers that move files into a sandbox as one upload.
package tarx

import (
	"archive/tar"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Pack writes hostPath (file or directory) to w as a tar stream whose
// entries are rooted at name.
func Pack(w io.Writer, hostPath, name string) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(hostPath, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(hostPath, p)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(filepath.Join(name, rel))
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}
