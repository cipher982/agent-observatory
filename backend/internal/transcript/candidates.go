package transcript

import (
	"os"
	"path/filepath"
)

// This file collects transcript file *paths* with their modification time, using
// only directory listings + DirEntry.Info() (a stat) — never reading file
// contents. DiscoverRecent ranks these by mtime and fully parses only the most
// recent N, which keeps the interactive/polled API fast even with thousands of
// transcripts on disk.

func claudeCandidates(home string) []candidate {
	root := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []candidate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		projDir := filepath.Join(root, e.Name())
		files, err := os.ReadDir(projDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || filepath.Ext(f.Name()) != ".jsonl" {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			out = append(out, candidate{
				path:    filepath.Join(projDir, f.Name()),
				runtime: "claude",
				mtime:   info.ModTime(),
			})
		}
	}
	return out
}

func codexCandidates(home string) []candidate {
	root := filepath.Join(home, ".codex", "sessions")
	var out []candidate
	// Codex nests by YYYY/MM/DD; WalkDir but only stat .jsonl leaves.
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".jsonl" {
			return nil // skip legacy flat *.json single-object rollouts
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, candidate{path: path, runtime: "codex", mtime: info.ModTime()})
		return nil
	})
	return out
}

// antigravityCandidates lists Antigravity conversation files (opaque .pb) by
// mtime. cwd is resolved later from history.jsonl, not from the file.
func antigravityCandidates(home string) []candidate {
	convDir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	entries, err := os.ReadDir(convDir)
	if err != nil {
		return nil
	}
	var out []candidate
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".pb" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, candidate{
			path:    filepath.Join(convDir, e.Name()),
			runtime: "antigravity",
			mtime:   info.ModTime(),
		})
	}
	return out
}
