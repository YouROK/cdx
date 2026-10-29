package pluginapi

// File operations available to plugins.
//
// The model never sees these methods: a plugin uses them to build its own
// tools. Paths resolve inside the working directory and permissions are
// checked on every call.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// FileInfo describes a file.
type FileInfo struct {
	// Path is relative to the working directory.
	Path  string `json:"path"`
	Name  string `json:"name"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	Mode  string `json:"mode"`
	MTime int64  `json:"mtime"`
}

// ReadResult is the read content.
type ReadResult struct {
	Path string `json:"path"`
	Text string `json:"text"`
	// Truncated reports that the file exceeds the limit and was cut.
	Truncated bool  `json:"truncated"`
	Size      int64 `json:"size"`
}

// WriteResult is the outcome of a write.
type WriteResult struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
	// Created reports that the file did not exist before.
	Created bool `json:"created"`
}

// EditResult is the outcome of a fragment replacement.
type EditResult struct {
	Path     string `json:"path"`
	Replaced int    `json:"replaced"`
}

// Match is one found line.
type Match struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// SearchResult is the outcome of a content search.
type SearchResult struct {
	Matches []Match `json:"matches"`
	// Truncated reports that more matches exist than the limit allows.
	Truncated bool `json:"truncated"`
}

// SearchOptions configures a search.
type SearchOptions struct {
	// Path narrows the search to a directory or file; empty means the root.
	Path string `json:"path"`
	// Glob narrows the file set by name pattern; empty means every file.
	Glob string `json:"glob"`
	// Regex treats the query as a regular expression.
	//
	// The engine is Go's RE2: no lookaround and no backreferences.
	Regex bool `json:"regex"`
	// IgnoreCase matches without regard to letter case.
	IgnoreCase bool `json:"ignore_case"`
}

// Read returns the content of a file.
func (a *API) Read(path string) (*ReadResult, error) {
	full, err := a.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := a.allow("system.fs.readFile", "read", "read "+path); err != nil {
		return nil, err
	}

	st, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%q is a directory", path)
	}

	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Only the limit is read: a huge file would not fit the model context
	// anyway, so there is no reason to spend the memory.
	data, err := io.ReadAll(io.LimitReader(f, int64(a.limits.MaxReadBytes)))
	if err != nil {
		return nil, err
	}

	// A binary file is useless in the context, so say so instead.
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%q is not a text file", path)
	}

	return &ReadResult{
		Path:      path,
		Text:      string(data),
		Truncated: st.Size() > int64(len(data)),
		Size:      st.Size(),
	}, nil
}

// Write creates a file or replaces its content.
func (a *API) Write(path, content string) (*WriteResult, error) {
	full, err := a.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := a.allow("system.fs.writeFile", "edit", "write "+path); err != nil {
		return nil, err
	}

	_, statErr := os.Stat(full)
	created := os.IsNotExist(statErr)

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return &WriteResult{Path: path, Bytes: len(content), Created: created}, nil
}

// Edit replaces a text fragment in a file.
//
// Without all the fragment must occur exactly once: otherwise the model does
// not know which occurrence it meant and may damage the file.
func (a *API) Edit(path, old, replacement string, all bool) (*EditResult, error) {
	full, err := a.resolve(path)
	if err != nil {
		return nil, err
	}
	if old == "" {
		return nil, fmt.Errorf("the fragment to replace must not be empty")
	}
	if err := a.allow("system.fs.writeFile", "edit", "edit "+path); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	text := string(data)

	count := strings.Count(text, old)
	if count == 0 {
		return nil, fmt.Errorf("the fragment was not found in %q", path)
	}
	if count > 1 && !all {
		return nil, fmt.Errorf("the fragment occurs %d times in %q; pass all=true or use a longer fragment", count, path)
	}

	replaced := count
	if all {
		text = strings.ReplaceAll(text, old, replacement)
	} else {
		text = strings.Replace(text, old, replacement, 1)
		replaced = 1
	}

	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		return nil, err
	}
	return &EditResult{Path: path, Replaced: replaced}, nil
}

// List returns the entries of a directory.
func (a *API) List(path string) ([]FileInfo, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	full, err := a.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := a.allow("system.fs.list", "read", "list "+path); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}

	out := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, FileInfo{
			Path:  filepath.Join(path, e.Name()),
			Name:  e.Name(),
			Dir:   e.IsDir(),
			Size:  info.Size(),
			Mode:  info.Mode().String(),
			MTime: info.ModTime().Unix(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		// Directories come first, which is how such a list is read.
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Stat returns details of a path.
func (a *API) Stat(path string) (*FileInfo, error) {
	full, err := a.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := a.allow("system.fs.stat", "read", "stat "+path); err != nil {
		return nil, err
	}

	info, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	return &FileInfo{
		Path:  path,
		Name:  info.Name(),
		Dir:   info.IsDir(),
		Size:  info.Size(),
		Mode:  info.Mode().String(),
		MTime: info.ModTime().Unix(),
	}, nil
}

// Exists reports whether a path exists.
func (a *API) Exists(path string) bool {
	full, err := a.resolve(path)
	if err != nil {
		return false
	}
	_, err = os.Stat(full)
	return err == nil
}

// Mkdir creates a directory with all its parents.
func (a *API) Mkdir(path string) error {
	full, err := a.resolve(path)
	if err != nil {
		return err
	}
	if err := a.allow("system.fs.mkdir", "edit", "create directory "+path); err != nil {
		return err
	}
	return os.MkdirAll(full, 0o755)
}

// Remove deletes a file or an empty directory.
//
// Recursive removal is not supported: it is a dangerous operation and must
// not be a way around the shell rules.
func (a *API) Remove(path string) error {
	full, err := a.resolve(path)
	if err != nil {
		return err
	}
	if err := a.allow("system.fs.remove", "edit", "remove "+path); err != nil {
		return err
	}

	st, err := os.Stat(full)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return os.Remove(full)
	}
	return os.Remove(full)
}

// Glob returns paths matching a pattern.
//
// Go path patterns apply: "*" stays within one directory, "**" spans any
// depth.
func (a *API) Glob(pattern string) ([]string, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	if err := a.allow("system.fs.glob", "read", "search files by "+pattern); err != nil {
		return nil, err
	}

	// filepath.Glob has no "**", so such a pattern is expanded by a walk.
	if !strings.Contains(pattern, "**") {
		full := filepath.Join(a.rootAt(), pattern)
		matches, err := filepath.Glob(full)
		if err != nil {
			return nil, err
		}
		return a.relative(matches), nil
	}

	base, rest := splitDoubleStar(pattern)
	base_root := a.rootAt()
	root := base_root
	if base != "" {
		root = filepath.Join(base_root, base)
	}

	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Service directories are skipped: they hold thousands of files.
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(base_root, path)
		if err != nil {
			return nil
		}
		if matched, _ := filepath.Match(rest, filepath.Base(path)); matched {
			out = append(out, rel)
			if len(out) >= a.limits.MaxResults {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// Search looks for a string inside files.
//
// The regex engine is Go's RE2: character classes and groups work, but not
// lookaround or backreferences.
func (a *API) Search(query string, opts SearchOptions) (*SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if err := a.allow("system.fs.search", "read", "search for "+query); err != nil {
		return nil, err
	}

	matcher, err := buildMatcher(query, opts)
	if err != nil {
		return nil, err
	}

	start := a.rootAt()
	if opts.Path != "" {
		start, err = a.resolve(opts.Path)
		if err != nil {
			return nil, err
		}
	}

	files, err := a.filesToSearch(start, opts.Glob)
	if err != nil {
		return nil, err
	}

	res := &SearchResult{Matches: []Match{}}
	for _, file := range files {
		if len(res.Matches) >= a.limits.MaxResults {
			res.Truncated = true
			break
		}
		a.searchFile(file, matcher, res)
	}
	return res, nil
}

// matcher reports whether a line matches.
type matcher func(string) bool

// buildMatcher builds the line predicate from the search options.
func buildMatcher(query string, opts SearchOptions) (matcher, error) {
	if !opts.Regex {
		needle := query
		if opts.IgnoreCase {
			needle = strings.ToLower(needle)
			return func(s string) bool { return strings.Contains(strings.ToLower(s), needle) }, nil
		}
		return func(s string) bool { return strings.Contains(s, needle) }, nil
	}

	pattern := query
	if opts.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression (RE2): %w", err)
	}
	return re.MatchString, nil
}

// filesToSearch collects the files to scan.
func (a *API) filesToSearch(start, glob string) ([]string, error) {
	info, err := os.Stat(start)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{start}, nil
	}

	var out []string
	err = filepath.WalkDir(start, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if glob != "" {
			if matched, _ := filepath.Match(glob, filepath.Base(path)); !matched {
				return nil
			}
		}
		// Files above the read limit are skipped, they cannot be text here.
		if st, err := d.Info(); err == nil && st.Size() > int64(a.limits.MaxReadBytes) {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out, err
}

// searchFile appends the matches found in one file.
func (a *API) searchFile(path string, match matcher, res *SearchResult) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	rel, err := filepath.Rel(a.rootAt(), path)
	if err != nil {
		rel = path
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), a.limits.MaxReadBytes)

	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()

		// A binary file is skipped entirely: matches in it mean nothing and
		// would only put noise into the model context.
		if line == 1 && !isTextLine(text) {
			return
		}
		if !match(text) {
			continue
		}
		res.Matches = append(res.Matches, Match{Path: rel, Line: line, Text: strings.TrimSpace(text)})
		if len(res.Matches) >= a.limits.MaxResults {
			return
		}
	}
}

// relative turns absolute paths into paths from the root.
func (a *API) relative(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, err := filepath.Rel(a.rootAt(), p)
		if err != nil {
			rel = p
		}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// splitDoubleStar splits a pattern into a base directory and a remainder.
func splitDoubleStar(pattern string) (base, rest string) {
	idx := strings.Index(pattern, "**")
	if idx < 0 {
		return "", pattern
	}
	base = strings.TrimSuffix(pattern[:idx], string(filepath.Separator))
	rest = strings.TrimPrefix(pattern[idx+2:], string(filepath.Separator))
	return base, rest
}

// isTextLine reports whether a line looks like text.
//
// A NUL byte or invalid UTF-8 in the first line marks a binary file.
func isTextLine(line string) bool {
	if strings.ContainsRune(line, 0) {
		return false
	}
	return utf8.ValidString(line)
}
