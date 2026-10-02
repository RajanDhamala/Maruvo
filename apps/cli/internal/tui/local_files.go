package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type localFile struct {
	path, label string
	directory   bool
}

func projectDirectory() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}

	for dir := cwd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}

		if filepath.Dir(dir) == dir {
			return cwd
		}
	}
}

// Only a word starting with @ opens completion; email addresses stay text.
func attachmentToken(field textField) (query string, start, end int, ok bool) {
	runes := []rune(field.value)
	end = min(field.cursor, len(runes))

	start = end
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}

	if start == end || runes[start] != '@' {
		return "", 0, 0, false
	}

	query = string(runes[start+1 : end])
	for end < len(runes) && !unicode.IsSpace(runes[end]) {
		end++
	}

	return query, start, end, true
}

func localFileSuggestions(root, query string) ([]localFile, error) {
	directory, needle := root, query

	direct := query == "" || strings.Contains(query, string(filepath.Separator))
	if strings.HasPrefix(query, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}

		query = filepath.Join(home, query[2:])
	}

	if direct && query != "" {
		directory, needle = filepath.Split(query)
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(root, directory)
		}
	}

	needle = strings.ToLower(needle)

	var matches []localFile

	add := func(path string, entry fs.DirEntry) {
		if (strings.HasPrefix(entry.Name(), ".") && !strings.HasPrefix(needle, ".")) ||
			!strings.Contains(strings.ToLower(entry.Name()), needle) {
			return
		}

		if !entry.IsDir() && !entry.Type().IsRegular() {
			return
		}

		label, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(label, ".."+string(filepath.Separator)) {
			label = path
		}

		matches = append(matches, localFile{path: path, label: label, directory: entry.IsDir()})
	}

	if direct {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			add(filepath.Join(directory, entry.Name()), entry)
		}
	} else {
		visited := 0

		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}

			visited++
			if visited > 15000 || len(matches) >= 200 {
				return fs.SkipAll
			}

			if entry.IsDir() {
				switch entry.Name() {
				case "node_modules", "target", "vendor", "bin":
					return fs.SkipDir
				}

				if path != root && strings.HasPrefix(entry.Name(), ".") {
					return fs.SkipDir
				}

				return nil
			}

			add(path, entry)

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		a := strings.HasPrefix(strings.ToLower(filepath.Base(matches[i].path)), needle)

		b := strings.HasPrefix(strings.ToLower(filepath.Base(matches[j].path)), needle)
		if a != b {
			return a
		}

		return matches[i].label < matches[j].label
	})

	return matches[:min(12, len(matches))], nil
}
