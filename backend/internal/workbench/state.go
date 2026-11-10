package workbench

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}
type Thread struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	Title     string    `json:"title"`
	Created   time.Time `json:"created"`
}
type Receipt struct {
	ThreadID  string          `json:"threadId"`
	Digest    string          `json:"digest"`
	Result    json.RawMessage `json:"result,omitempty"`
	Uncertain bool            `json:"uncertain,omitempty"`
}
type State struct {
	Projects []Project          `json:"projects"`
	Threads  []Thread           `json:"threads"`
	Receipts map[string]Receipt `json:"receipts"`
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func loadState(dir string) (State, error) {
	s := State{Projects: []Project{}, Threads: []Thread{}, Receipts: map[string]Receipt{}}
	b, err := os.ReadFile(filepath.Join(dir, "console.json"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	if s.Receipts == nil {
		s.Receipts = map[string]Receipt{}
	}
	return s, err
}
func saveState(dir string, s State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".console-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, filepath.Join(dir, "console.json"))
}
func canonicalDir(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("absolute directory required")
	}
	p, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("not a directory")
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	f.Close()
	return p, nil
}
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}
func allowedPath(path string, roots []string) (string, error) {
	p, err := canonicalDir(path)
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		r, e := canonicalDir(root)
		if e == nil && within(r, p) {
			return p, nil
		}
	}
	return "", errors.New("directory outside authorized roots")
}
