package app

// A small file browser for the /data folder of an app: list a folder, download
// a file, upload or replace a file, make a folder, delete a file or folder.
// The work is done by throwaway helper containers (dockerx/volfiles.go), never
// inside the app container. Every path is relative to /data and checked here
// first (cleanFilePath, validFileName).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	// maxFileUpload is the biggest single file that can be uploaded.
	maxFileUpload = 100 << 20
	// maxFileEntries is how many items of one folder are listed.
	maxFileEntries = 1000

	maxFilePathLen = 4096
	maxFileNameLen = 255
)

// fileStore reads and changes the files of a data volume. Paths are relative
// to /data and already checked. The Docker client does it for real
// (dockerVolumes); tests use a fake. A volumeStore that is not a fileStore
// cannot browse files.
type fileStore interface {
	ListFiles(ctx context.Context, volume, dir string, max int) (dockerx.DirListing, error)
	OpenFile(ctx context.Context, volume, rel string) (io.ReadCloser, int64, time.Time, error)
	PutFile(ctx context.Context, volume, dir, name string, size int64, body io.Reader, overwrite bool) error
	MakeDir(ctx context.Context, volume, dir, name string) error
	RemoveFile(ctx context.Context, volume, dir, name string) error
}

func (d dockerVolumes) ListFiles(ctx context.Context, volume, dir string, max int) (dockerx.DirListing, error) {
	return d.c.ListVolumeDir(ctx, volume, dir, max)
}

func (d dockerVolumes) OpenFile(ctx context.Context, volume, rel string) (io.ReadCloser, int64, time.Time, error) {
	f, err := d.c.OpenVolumeFile(ctx, volume, rel)
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	return f, f.Size, f.ModTime, nil
}

func (d dockerVolumes) PutFile(ctx context.Context, volume, dir, name string, size int64, body io.Reader, overwrite bool) error {
	return d.c.PutVolumeFile(ctx, volume, dir, name, size, body, overwrite)
}

func (d dockerVolumes) MakeDir(ctx context.Context, volume, dir, name string) error {
	return d.c.MkdirVolume(ctx, volume, dir, name)
}

func (d dockerVolumes) RemoveFile(ctx context.Context, volume, dir, name string) error {
	return d.c.RemoveVolumePath(ctx, volume, dir, name)
}

// FileEntry is one item of a folder in /data.
type FileEntry struct {
	Name       string    `json:"name"`
	Type       string    `json:"type" enum:"file,dir,symlink,other" doc:"file, dir, symlink or other (a pipe or socket)"`
	Size       int64     `json:"size" doc:"Bytes; 0 for everything but a file"`
	ModifiedAt time.Time `json:"modified_at"`
}

// FileList is one folder of /data.
type FileList struct {
	Path           string      `json:"path" doc:"The folder, relative to /data; empty for /data itself"`
	Items          []FileEntry `json:"items" doc:"Folders first, then by name"`
	Truncated      bool        `json:"truncated" doc:"There are more items in this folder than the list holds"`
	UploadMaxBytes int64       `json:"upload_max_bytes" doc:"The biggest file that can be uploaded now"`
}

// FileDownload is a file being sent to the user. Close it.
type FileDownload struct {
	Body    io.ReadCloser
	Name    string
	Size    int64
	ModTime time.Time
}

// ---- paths ----

func filePathErr(msg string) *apperr.Error {
	return apperr.BadRequest("path_invalid", msg, "Use a path inside /data, like backups/2026 or config.json.")
}

// checkFileText rejects text that can not be a name in /data: empty is the
// caller's business, control characters (a line break would break the folder
// list), invalid UTF-8 and too long.
func checkFileText(s string, max int) *apperr.Error {
	if len(s) > max {
		return filePathErr("This path is too long.")
	}
	if !utf8.ValidString(s) {
		return filePathErr("This path is not valid text.")
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return filePathErr("This path has a control character in it.")
		}
	}
	return nil
}

// cleanFilePath turns a path the user sent into a clean path relative to /data
// ("" is /data itself). A leading slash is the same as none. Any ".." is
// refused, not resolved: it can only be a mistake or an attempt to get out.
func cleanFilePath(p string) (string, error) {
	if err := checkFileText(p, maxFilePathLen); err != nil {
		return "", err
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", filePathErr("A path may not contain \"..\".")
		}
		if len(seg) > maxFileNameLen {
			return "", filePathErr("A name in this path is too long.")
		}
	}
	rel := strings.TrimPrefix(path.Clean("/"+p), "/")
	return rel, nil
}

// validFileName checks the name of a new file or folder.
func validFileName(n string) error {
	switch {
	case n == "":
		return apperr.BadRequest("name_missing", "There is no name.", "Give the name of the file or folder.")
	case n == "." || n == "..":
		return apperr.BadRequest("name_invalid", fmt.Sprintf("%s is not a valid name.", quote(n)), "Use a name like data.json.")
	case strings.Contains(n, "/"):
		return apperr.BadRequest("name_invalid", "A name may not contain a slash.", "Put the folder in path and send only the name.")
	case len(n) > maxFileNameLen:
		return apperr.BadRequest("name_invalid", "This name is too long.", "Use at most 255 bytes.")
	}
	if err := checkFileText(n, maxFileNameLen); err != nil {
		return apperr.BadRequest("name_invalid", "This name is not valid.", "Use plain letters, numbers and punctuation.")
	}
	return nil
}

// ---- common steps ----

func filesVolumeOffErr() *apperr.Error {
	return apperr.Conflict("volume_off", "Saved files (/data) are turned off for this app, so there are no files to browse.",
		"Turn them on with update_app volume=true, or in the dashboard under Storage.")
}

func filesBusyErr() *apperr.Error {
	return apperr.Conflict("files_busy", "A backup, a restore or another change to the files of this app is running.",
		"Wait until it has finished, then try again.")
}

// filesTarget loads the app and the store for its volume. The files are the
// owner's data: only the owner (or the system) gets to them, never an admin
// who looks at another user's app.
// volume names one volume of an app with several; "" is the first.
func (s *Service) filesTarget(ctx context.Context, a Actor, ref string, write bool, volume string) (db.App, db.User, fileStore, volumeSpec, error) {
	row, owner, err := s.backupApp(ctx, a, ref, write)
	if err != nil {
		return db.App{}, db.User{}, nil, volumeSpec{}, err
	}
	if row.UserID != a.UserID && a.Channel != ChannelSystem {
		return db.App{}, db.User{}, nil, volumeSpec{}, appNotFound(ref)
	}
	vol, ok, err := s.pickVolume(ctx, row, volume)
	if err != nil {
		return db.App{}, db.User{}, nil, volumeSpec{}, err
	}
	if !ok {
		return db.App{}, db.User{}, nil, volumeSpec{}, filesVolumeOffErr()
	}
	fail := func(err error) (db.App, db.User, fileStore, volumeSpec, error) {
		return db.App{}, db.User{}, nil, volumeSpec{}, err
	}
	vs, err := s.volumes()
	if err != nil {
		return fail(err)
	}
	fs, ok := vs.(fileStore)
	if !ok {
		return fail(apperr.Unavailable("docker_unavailable", "bitploy cannot reach the files in /data.", "Try again in a minute."))
	}
	if has, err := vs.HasVolume(ctx, vol.VolumeName); err != nil {
		return fail(apperr.Unavailable("docker_unavailable", "bitploy could not read the files in /data.", "Try again in a minute."))
	} else if !has {
		return fail(volumeNotReadyErr())
	}
	// While a backup is restored the folder is emptied and filled again.
	s.backup.mu.Lock()
	r := s.backup.restore[row.ID]
	restoring := r != nil && r.State == "running"
	s.backup.mu.Unlock()
	if restoring {
		return fail(backupBusyErr())
	}
	return row, owner, fs, vol, nil
}

// withFilesLock runs a change of the files. It holds the backup lock, so no
// backup or restore starts meanwhile (and the other way round), and the app
// lock, so no deployment swaps containers meanwhile.
func (s *Service) withFilesLock(row db.App, fn func() error) error {
	lock := s.backupLock(row.ID)
	if !lock.TryLock() {
		return filesBusyErr()
	}
	defer lock.Unlock()
	al := s.appLock(row.ID)
	al.Lock()
	defer al.Unlock()
	if s.appBusy(row.ID) {
		return busyErr()
	}
	return fn()
}

// filesErr turns an error of the file store into plain words.
func filesErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, dockerx.ErrNoVolume):
		return volumeNotReadyErr()
	case errors.Is(err, dockerx.ErrNotFound):
		return apperr.NotFound("file_not_found", "There is no such file or folder in /data.", "Reload the list: it may have been deleted.")
	case errors.Is(err, dockerx.ErrNotDir):
		return apperr.Conflict("not_a_folder", "This is a file, not a folder.", "Choose a folder.")
	case errors.Is(err, dockerx.ErrLinkInPath):
		return apperr.Unprocessable("file_symlink", "This path goes through a symbolic link. bitploy does not follow links in /data.",
			"Use the real folder, not the link.")
	case errors.Is(err, dockerx.ErrExists):
		return apperr.Conflict("file_exists", "A file or folder with this name already exists.", "Choose another name.")
	case errors.Is(err, dockerx.ErrWrongKind):
		return apperr.Unprocessable("not_a_file", "Only regular files can be downloaded or replaced, not folders or special files.", "Pick a file.")
	case errors.Is(err, context.Canceled):
		return apperr.BadRequest("request_cancelled", "The request was cancelled.", "Try again.")
	case errors.Is(err, context.DeadlineExceeded):
		return apperr.Unavailable("files_timeout", "This took too long and was stopped.", "Try again, or try a smaller file.")
	}
	msg := apperr.Redact(dockerx.DescribeError(err))
	if len([]rune(msg)) > 300 {
		msg = string([]rune(msg)[:300]) + "..."
	}
	return apperr.Unavailable("files_unavailable", "bitploy could not reach the files in /data: "+msg, "Try again in a minute.").WithCause(err)
}

// fileUploadLimit is the biggest file that may be uploaded now: at most
// maxFileUpload and no more than what is left of the /data limit of the app
// (measured every few minutes). The /data limit is already part of the
// owner's storage allowance, so the allowance needs no check of its own.
// roomBound says it is the free space that sets the limit.
func (s *Service) fileUploadLimit(ctx context.Context, row db.App, vol volumeSpec) (limit int64, roomBound bool) {
	room := row.VolumeMb * mib
	if isMulti(row) {
		room = vol.SizeMB*mib - vol.UsedBytes
	} else if d, err := s.Q.GetAppDisk(ctx, row.ID); err == nil {
		room -= d.VolumeBytes
	}
	if room < 0 {
		room = 0
	}
	if room < maxFileUpload {
		return room, true
	}
	return maxFileUpload, false
}

// ---- list ----

// ListFiles lists a folder of /data.
func (s *Service) ListFiles(ctx context.Context, a Actor, ref, dir string, volume ...string) (FileList, error) {
	rel, err := cleanFilePath(dir)
	if err != nil {
		return FileList{}, err
	}
	row, _, fs, vol, err := s.filesTarget(ctx, a, ref, false, firstOpt(volume))
	if err != nil {
		return FileList{}, err
	}
	l, err := fs.ListFiles(ctx, vol.VolumeName, rel, maxFileEntries)
	if err != nil {
		return FileList{}, filesErr(err)
	}
	out := FileList{Path: rel, Items: make([]FileEntry, 0, len(l.Entries)), Truncated: l.Truncated}
	for _, e := range l.Entries {
		out.Items = append(out.Items, FileEntry{Name: e.Name, Type: e.Kind, Size: e.Size, ModifiedAt: e.Mtime})
	}
	out.UploadMaxBytes, _ = s.fileUploadLimit(ctx, row, vol)
	return out, nil
}

// ---- download ----

// OpenFile opens a file of /data for download. Like a backup download it
// needs the deploy scope: a read-only token may see the names but not take the
// data. The app keeps running, so a file that is being written can come out
// cut off or inconsistent (a database: stop the app first).
func (s *Service) OpenFile(ctx context.Context, a Actor, ref, file string, volume ...string) (*FileDownload, error) {
	rel, err := cleanFilePath(file)
	if err != nil {
		return nil, err
	}
	if rel == "" {
		return nil, filesErr(dockerx.ErrWrongKind)
	}
	row, _, fs, vol, err := s.filesTarget(ctx, a, ref, true, firstOpt(volume))
	if err != nil {
		return nil, err
	}
	body, size, mtime, err := fs.OpenFile(ctx, vol.VolumeName, rel)
	if err != nil {
		return nil, filesErr(err)
	}
	s.Audit(ctx, a, "files.download", "app", row.ID, map[string]any{"path": rel, "bytes": size})
	return &FileDownload{Body: body, Name: path.Base(rel), Size: size, ModTime: mtime}, nil
}

// ---- upload ----

// UploadFile stores the body as the file name in the folder dir. An existing
// file is only replaced with overwrite. contentLength is the size announced
// by the client, or -1.
func (s *Service) UploadFile(ctx context.Context, a Actor, ref, dir, name string, overwrite bool, body io.Reader, contentLength int64, volume ...string) (FileEntry, error) {
	rel, err := cleanFilePath(dir)
	if err != nil {
		return FileEntry{}, err
	}
	if err := validFileName(name); err != nil {
		return FileEntry{}, err
	}
	row, _, fs, vol, err := s.filesTarget(ctx, a, ref, true, firstOpt(volume))
	if err != nil {
		return FileEntry{}, err
	}
	if ok, err := s.DiskHasRoom(); err == nil && !ok {
		return FileEntry{}, apperr.Unavailable("server_disk_full", "The server is almost out of disk space, so nothing can be uploaded.",
			"Tell the bitploy admin that the server disk is full. Your apps are not affected.")
	}
	limit, roomBound := s.fileUploadLimit(ctx, row, vol)
	tooBig := func() *apperr.Error {
		if roomBound {
			return apperr.New(413, "volume_full",
				fmt.Sprintf("There is not enough room left in /data (the limit is %d MB).", vol.SizeMB),
				"Delete files you no longer need, or raise the /data limit of the app.")
		}
		return apperr.New(413, "file_too_large", fmt.Sprintf("Files can be up to %d MB.", maxFileUpload>>20),
			"Upload a smaller file.")
	}
	if limit <= 0 || contentLength > limit {
		return FileEntry{}, tooBig()
	}

	// The size must be known before the file goes into the volume: keep it on
	// disk until it has arrived completely (at most the limit).
	if err := os.MkdirAll(s.Cfg.Path("tmp"), 0o750); err != nil {
		return FileEntry{}, apperr.Internal(err)
	}
	tmp, err := os.CreateTemp(s.Cfg.Path("tmp"), "file-*")
	if err != nil {
		return FileEntry{}, apperr.Internal(err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	size, err := io.Copy(tmp, &capReader{r: body, left: limit})
	var re *readErr
	switch {
	case errors.Is(err, errUploadTooLarge):
		return FileEntry{}, tooBig()
	case errors.As(err, &re), errors.Is(err, context.Canceled):
		return FileEntry{}, apperr.BadRequest("upload_interrupted", "The upload was cut off before it finished.", "Try again.")
	case err != nil:
		return FileEntry{}, apperr.Internal(err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return FileEntry{}, apperr.Internal(err)
	}

	err = s.withFilesLock(row, func() error {
		return filesErr(fs.PutFile(ctx, vol.VolumeName, rel, name, size, tmp, overwrite))
	})
	if err != nil {
		return FileEntry{}, err
	}
	full := path.Join(rel, name)
	s.Audit(ctx, a, "files.upload", "app", row.ID, map[string]any{"path": full, "bytes": size, "overwrite": overwrite})
	s.metricsMgr().MeasureSoon()
	return FileEntry{Name: name, Type: "file", Size: size, ModifiedAt: s.Now()}, nil
}

// ---- folders and delete ----

// MakeFolder creates the folder name inside the folder dir.
func (s *Service) MakeFolder(ctx context.Context, a Actor, ref, dir, name string, volume ...string) (FileEntry, error) {
	rel, err := cleanFilePath(dir)
	if err != nil {
		return FileEntry{}, err
	}
	if err := validFileName(name); err != nil {
		return FileEntry{}, err
	}
	row, _, fs, vol, err := s.filesTarget(ctx, a, ref, true, firstOpt(volume))
	if err != nil {
		return FileEntry{}, err
	}
	err = s.withFilesLock(row, func() error {
		return filesErr(fs.MakeDir(ctx, vol.VolumeName, rel, name))
	})
	if err != nil {
		return FileEntry{}, err
	}
	s.Audit(ctx, a, "files.mkdir", "app", row.ID, map[string]any{"path": path.Join(rel, name)})
	return FileEntry{Name: name, Type: "dir", ModifiedAt: s.Now()}, nil
}

// DeleteFile deletes a file, a link (not what it points to) or a folder with
// everything in it. /data itself can not be deleted.
func (s *Service) DeleteFile(ctx context.Context, a Actor, ref, target string, volume ...string) error {
	rel, err := cleanFilePath(target)
	if err != nil {
		return err
	}
	if rel == "" {
		return apperr.BadRequest("path_invalid", "The /data folder itself cannot be deleted.", "Choose a file or folder inside it.")
	}
	row, _, fs, vol, err := s.filesTarget(ctx, a, ref, true, firstOpt(volume))
	if err != nil {
		return err
	}
	dir, name := path.Split(rel)
	dir = strings.TrimSuffix(dir, "/")
	err = s.withFilesLock(row, func() error {
		return filesErr(fs.RemoveFile(ctx, vol.VolumeName, dir, name))
	})
	if err != nil {
		return err
	}
	s.Audit(ctx, a, "files.delete", "app", row.ID, map[string]any{"path": rel})
	s.metricsMgr().MeasureSoon()
	return nil
}
