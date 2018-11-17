package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WalkTree walks the tree starting from root, returning
// all directories and files found. If maxDepth is > 0,
// the walk will truncate below this many levels. Directories
// in the excludeDirs slice will be ignored.
func WalkTree(root string, excludeDirs []string, maxdepth int) ([]string, []string, error) {
	dirs := []string{}
	files := []string{}

	currDepth := func(path string) int {
		depth, _ := DirDepth(root, path)
		return depth
	}

	err := filepath.Walk(
		root,
		func(path string, pathInfo os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if !pathInfo.IsDir() {
				files = append(files, path)
			} else {
				if maxdepth > 0 && currDepth(path) > maxdepth {
					return filepath.SkipDir
				}

				for _, e := range excludeDirs {
					if pathInfo.Name() == e {
						return filepath.SkipDir
					}
				}

				dirs = append(dirs, path)
			}

			return nil
		},
	)

	return dirs, files, err
}

// FindDirs finds all directories matching a given dir name
// glob, or exact name, below the given start directory.
// The search goes at most max depth directories down.
func FindDirs(startDir, dirNameGlob string, maxDepth int, ignore []string) ([]string, error) {
	dirs, _, err := WalkTree(startDir, ignore, maxDepth)
	var matches []string
	for _, d := range dirs {
		matched, _ := filepath.Match(dirNameGlob, filepath.Base(d))
		if matched {
			matches = append(matches, d)
		}
	}
	return matches, err
}

// FindFiles finds all files matching a given file name glob, or exact name,
// below the given start directory. The search goes at most max depth
// directories down.
func FindFiles(startDir, fileNameGlob string, maxDepth int, ignore []string) ([]string, error) {
	_, files, err := WalkTree(startDir, ignore, maxDepth)
	var matches []string
	for _, f := range files {
		matched, _ := filepath.Match(fileNameGlob, filepath.Base(f))
		if matched {
			matches = append(matches, f)
		}
	}
	return matches, err
}

// RemoveFiles will delete files matching the given file name glob,
// found at most maxDepth directories below startDir
func RemoveFiles(startDir, fileNameGlob string, maxDepth int, ignore []string) error {
	files, err := FindFiles(startDir, fileNameGlob, maxDepth, ignore)
	if err != nil {
		return err
	}

	for _, file := range files {
		os.Remove(file)
	}

	return nil
}

// DirTreeSize walks the tree starting at root directory,
// and totals the size of all files it finds. Directories
// matching entries in the excludeDirs list are not traversed.
// The grand total in bytes is returned.
func DirTreeSize(root string, excludeDirs []string) (int64, error) {
	totSize := int64(0)
	err := filepath.Walk(
		root,
		func(path string, pathInfo os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if pathInfo.IsDir() {
				for _, e := range excludeDirs {
					if pathInfo.Name() == e {
						return filepath.SkipDir
					}
				}
			} else {
				totSize += pathInfo.Size()
			}

			return nil
		},
	)

	return totSize, err
}

// DirDepth returns the integer number of directories that
// path is below root. If root is not a prefix of path, it
// returns 0. If path is a file, the depth is calculated with
// respect to the parent directory of the file.
func DirDepth(root, path string) (int, error) {
	removeTrailingSlash := func(s string) string {
		if strings.HasSuffix(s, "/") {
			s = s[:len(s)-1]
		}

		s, _ = filepath.Abs(s)
		return s
	}

	root = removeTrailingSlash(root)
	path = removeTrailingSlash(path)

	if root == path {
		return 0, nil
	}

	if !strings.HasPrefix(path, root) {
		return 0, fmt.Errorf("%s not a prefix of %s", root, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}

	if !info.IsDir() {
		path = filepath.Dir(path)
	}

	path = strings.Replace(path, root, "", 1)
	path = strings.Trim(path, "/")
	dirs := strings.Split(path, "/")
	return len(dirs), nil
}
