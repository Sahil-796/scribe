// Package seed implements `scribe init`'s seed pass: turn a repo scribe has
// never seen before into a first draft of PROJECT.md and DECISIONS.md,
// before any transcript exists to learn from (docs/PLAN.md, phase 02).
//
// The seed pass only looks at facts already sitting in the repo — README,
// package manifests, a pruned directory tree, and any docs already checked
// in — and hands them to the same kind of writer agent the live loop uses
// (internal/worker), one call per doc: see ProjectPrompt/DecisionsPrompt
// and ParseProject/ParseDecisions in prompt.go/parse.go. Run never writes a
// file: init is a dry run by default (docs/PLAN.md, phase 02: "Dry run by
// default, writing somewhere readable before it touches the repo"), so the
// decision to commit the seeded content belongs to the caller (cmd/, out of
// scope here), not to this package.
package seed

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// Facts is what the scan recovered from the repo.
type Facts struct {
	RepoRoot string
	Tree     string            // directory tree, depth-limited, noise dirs pruned
	Files    map[string]string // relative path -> contents, size-capped
}

const (
	// maxTreeDepth bounds how far the directory tree walk descends below
	// RepoRoot. Deep source trees exist to organize code for humans, not
	// to describe a project to a model — a few levels is enough to show
	// shape (is this a monorepo? where do packages live?) without paying
	// for every leaf file.
	maxTreeDepth = 4

	// maxTreeEntries caps how many lines the tree can contribute, so a
	// repo with thousands of small files can't blow the prompt budget on
	// the tree alone.
	maxTreeEntries = 400

	// maxFileBytes caps how much of any single file (README, manifest,
	// existing doc) is kept. Long READMEs and generated lockfile-adjacent
	// manifests exist; the model needs the gist, not the whole thing.
	maxFileBytes = 32 * 1024

	// maxTotalFileBytes caps the sum of all file contents fed to the
	// writer. The writer's Run(prompt string) is a single string over a
	// subprocess call (internal/writer); it has to be able to hold
	// whatever Scan hands it.
	maxTotalFileBytes = 256 * 1024
)

// noiseDirs are pruned everywhere in the tree, regardless of depth: build
// output, dependency caches, and scribe's own state — none of it describes
// the project, and dependency dirs in particular can be enormous.
var noiseDirs = map[string]bool{
	".git":          true,
	"node_modules":  true,
	"vendor":        true,
	"dist":          true,
	"build":         true,
	"bin":           true,
	".scribe":       true,
	".idea":         true,
	".vscode":       true,
	"target":        true,
	"__pycache__":   true,
	".venv":         true,
	"venv":          true,
	".pytest_cache": true,
	".mypy_cache":   true,
	".next":         true,
	".turbo":        true,
	"coverage":      true,
}

// manifestNames are the package-manifest files worth reading in full when
// present, across the ecosystems scribe is likely to meet. Lockfiles
// (go.sum, package-lock.json, Cargo.lock, ...) are deliberately excluded:
// they're generated, huge, and carry no information a human wrote on
// purpose.
var manifestNames = []string{
	"go.mod",
	"package.json",
	"Cargo.toml",
	"pyproject.toml",
	"setup.py",
	"requirements.txt",
	"Gemfile",
	"pom.xml",
	"build.gradle",
	"build.gradle.kts",
	"composer.json",
	"mix.exs",
}

// Scan gathers README, package manifests (go.mod, package.json, Cargo.toml,
// pyproject.toml, etc.), a pruned directory tree, and any existing docs.
//
// Scan never reads outside repoRoot: every path it touches is derived from
// walking repoRoot itself, and symlinks that would escape it are skipped
// (see walkTree). It never touches ~/.claude/ or any other user directory —
// the seed pass has no business with agent state, only repo state.
func Scan(repoRoot string) (Facts, error) {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return Facts{}, fmt.Errorf("seed: resolve repo root %s: %w", repoRoot, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Facts{}, fmt.Errorf("seed: stat repo root %s: %w", abs, err)
	}
	if !info.IsDir() {
		return Facts{}, fmt.Errorf("seed: repo root %s is not a directory", abs)
	}

	ignore := loadGitignore(abs)

	f := Facts{
		RepoRoot: abs,
		Files:    make(map[string]string),
	}

	tree, err := walkTree(abs, ignore)
	if err != nil {
		return Facts{}, fmt.Errorf("seed: walk directory tree: %w", err)
	}
	f.Tree = tree

	budget := maxTotalFileBytes
	addFile := func(rel string) {
		if budget <= 0 {
			return
		}
		content, ok := readCapped(filepath.Join(abs, rel), budget)
		if !ok {
			return
		}
		f.Files[filepath.ToSlash(rel)] = content
		budget -= len(content)
	}

	for _, rel := range findReadmes(abs, ignore) {
		addFile(rel)
	}
	for _, name := range manifestNames {
		if _, err := os.Stat(filepath.Join(abs, name)); err == nil {
			addFile(name)
		}
	}
	for _, rel := range findExistingDocs(abs) {
		addFile(rel)
	}

	return f, nil
}

// readCapped reads path, truncating to the smaller of maxFileBytes and the
// remaining total budget. Returns ok=false if the file can't be read (e.g.
// a broken symlink) — scan degrades gracefully rather than failing the
// whole pass over one unreadable file.
func readCapped(path string, budget int) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	limit := maxFileBytes
	if budget < limit {
		limit = budget
	}
	if len(b) <= limit {
		return string(b), true
	}
	return string(b[:limit]) + "\n...[truncated]", true
}

// findReadmes returns top-level README files (any case, any extension),
// relative to root. Scoped to the repo root only — a README three levels
// deep belongs to a subpackage, not the project as a whole.
func findReadmes(root string, ignore *gitignore) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if ignore.matches(name) {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(name), "README") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// findExistingDocs returns any docs scribe has already written for this
// repo (docs/scribe/*.md), so a re-run of init on a repo that already has
// some history isn't starting from nothing. Missing directory is not an
// error — most repos running init for the first time won't have one yet.
func findExistingDocs(root string) []string {
	dir := filepath.Join(root, scribe.DocsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, filepath.ToSlash(filepath.Join(scribe.DocsDir, e.Name())))
	}
	sort.Strings(out)
	return out
}

// walkTree renders a depth-limited, noise-pruned directory tree starting at
// root, one entry per line, indented by depth. It stops adding lines once
// maxTreeEntries is reached so a huge repo can't make the tree unbounded,
// and it never follows symlinks — a symlink pointing outside root must not
// let the scan read outside repoRoot.
func walkTree(root string, ignore *gitignore) (string, error) {
	var b strings.Builder
	count := 0
	truncated := false

	var walk func(dir string, relDir string, depth int) error
	walk = func(dir string, relDir string, depth int) error {
		if truncated {
			return nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			// Unreadable subdirectory (permissions, race): skip it, don't
			// fail the whole scan over one directory.
			return nil
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

		for _, e := range entries {
			name := e.Name()
			rel := name
			if relDir != "" {
				rel = relDir + "/" + name
			}

			if e.IsDir() {
				if noiseDirs[name] || ignore.matches(name) {
					continue
				}
			} else if ignore.matches(name) {
				continue
			}

			if e.Type()&fs.ModeSymlink != 0 {
				// Don't follow symlinks: a link inside the repo can point
				// anywhere on disk, and Scan must never read outside
				// repoRoot.
				continue
			}

			if count >= maxTreeEntries {
				truncated = true
				return nil
			}
			b.WriteString(strings.Repeat("  ", depth))
			if e.IsDir() {
				b.WriteString(name + "/\n")
			} else {
				b.WriteString(name + "\n")
			}
			count++

			if e.IsDir() && depth+1 < maxTreeDepth {
				if err := walk(filepath.Join(dir, name), rel, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err := walk(root, "", 0); err != nil {
		return "", err
	}
	if truncated {
		b.WriteString("...[tree truncated]\n")
	}
	return b.String(), nil
}

// gitignore is a deliberately cheap approximation: it only matches bare
// names (files or directories) listed verbatim in the repo root's
// .gitignore, not full gitignore glob/negation semantics. Full pattern
// matching (globs, anchoring, negation, nested .gitignore files) is more
// machinery than a one-shot seed scan justifies; this catches the common
// case (a name like "coverage" or "*.log" ... well, exact names) cheaply,
// and anything it misses is still bounded by maxTreeEntries/maxFileBytes.
type gitignore struct {
	names map[string]bool
}

func (g *gitignore) matches(name string) bool {
	if g == nil {
		return false
	}
	return g.names[name]
}

// loadGitignore reads <root>/.gitignore if present and extracts bare-name
// patterns (no wildcards, no slashes) for the cheap match above. Patterns
// with globs or path separators are skipped rather than mis-honored.
func loadGitignore(root string) *gitignore {
	f, err := os.Open(filepath.Join(root, ".gitignore"))
	if err != nil {
		return &gitignore{names: map[string]bool{}}
	}
	defer f.Close()

	names := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		line = strings.TrimSuffix(line, "/")
		if strings.ContainsAny(line, "*?[/") {
			continue
		}
		names[line] = true
	}
	return &gitignore{names: names}
}
