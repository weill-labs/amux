package diffcoverage

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type ChangedLine struct {
	File string
	Line int
	Text string
}

type block struct {
	startLine int
	endLine   int
	covered   bool
}

type Result struct {
	CoveredLines    int
	ExecutableLines int
	IgnoredLines    int
	Uncovered       []ChangedLine
}

func (r Result) Percent() float64 {
	if r.ExecutableLines == 0 {
		return 100
	}
	return float64(r.CoveredLines) * 100 / float64(r.ExecutableLines)
}

func Analyze(baseRef, profilePath, modulePath string) (Result, error) {
	diff, err := gitDiff(baseRef)
	if err != nil {
		return Result{}, err
	}

	profile, err := loadFile(profilePath)
	if err != nil {
		return Result{}, err
	}

	return AnalyzeContents(diff, profile, modulePath)
}

func AnalyzeContents(diff, profile []byte, modulePath string) (Result, error) {
	changed, err := parseChangedLines(diff)
	if err != nil {
		return Result{}, err
	}

	coverage, err := parseCoverageProfile(profile, modulePath)
	if err != nil {
		return Result{}, err
	}

	var result Result
	for _, line := range changed {
		blocks := coverage[line.File]
		matched := false
		covered := false
		for _, block := range blocks {
			if line.Line < block.startLine || line.Line > block.endLine {
				continue
			}
			matched = true
			covered = covered || block.covered
		}
		if matched {
			result.ExecutableLines++
			if covered {
				result.CoveredLines++
			} else {
				result.Uncovered = append(result.Uncovered, line)
			}
		} else {
			result.IgnoredLines++
		}
	}

	return result, nil
}

func gitDiff(baseRef string) ([]byte, error) {
	cmd := exec.Command("git", "diff", "--unified=0", fmt.Sprintf("%s...HEAD", baseRef), "--")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git diff against %s: %w\n%s", baseRef, err, bytes.TrimSpace(out))
	}
	return out, nil
}

func loadFile(path string) ([]byte, error) {
	out, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return out, nil
}

var hunkPattern = regexp.MustCompile(`@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func parseChangedLines(diff []byte) ([]ChangedLine, error) {
	var changed []ChangedLine
	scanner := bufio.NewScanner(bytes.NewReader(diff))

	var currentFile string
	var nextLine int
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			currentFile = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "@@"):
			match := hunkPattern.FindStringSubmatch(line)
			if match == nil {
				return nil, fmt.Errorf("parse diff hunk: %q", line)
			}
			parsed, err := strconv.Atoi(match[1])
			if err != nil {
				return nil, fmt.Errorf("parse diff line %q: %w", match[1], err)
			}
			nextLine = parsed
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			if strings.HasSuffix(currentFile, ".go") {
				changed = append(changed, ChangedLine{
					File: currentFile,
					Line: nextLine,
					Text: strings.TrimPrefix(line, "+"),
				})
			}
			nextLine++
		case strings.HasPrefix(line, " "):
			nextLine++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan diff: %w", err)
	}
	return changed, nil
}

func parseCoverageProfile(profile []byte, modulePath string) (map[string][]block, error) {
	coverage := make(map[string][]block)
	scanner := bufio.NewScanner(bytes.NewReader(profile))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("parse coverprofile line: %q", line)
		}

		fileAndRange := fields[0]
		count := fields[2]
		file, blockRange, ok := strings.Cut(fileAndRange, ":")
		if !ok {
			return nil, fmt.Errorf("parse coverprofile path: %q", line)
		}
		file = normalizeCoveragePath(file, modulePath)

		start, end, err := parseRange(blockRange)
		if err != nil {
			return nil, err
		}

		hitCount, err := strconv.Atoi(count)
		if err != nil {
			return nil, fmt.Errorf("parse coverprofile count %q: %w", count, err)
		}
		coverage[file] = append(coverage[file], block{
			startLine: start,
			endLine:   end,
			covered:   hitCount > 0,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan coverprofile: %w", err)
	}
	return coverage, nil
}

func normalizeCoveragePath(path, modulePath string) string {
	if modulePath != "" {
		path = strings.TrimPrefix(path, modulePath+"/")
	}
	return strings.TrimPrefix(path, "./")
}

func parseRange(blockRange string) (int, int, error) {
	start, end, ok := strings.Cut(blockRange, ",")
	if !ok {
		return 0, 0, fmt.Errorf("parse coverprofile range %q", blockRange)
	}
	startLine, err := parseLineRef(start)
	if err != nil {
		return 0, 0, err
	}
	endLine, err := parseLineRef(end)
	if err != nil {
		return 0, 0, err
	}
	return startLine, endLine, nil
}

func parseLineRef(ref string) (int, error) {
	line, _, ok := strings.Cut(ref, ".")
	if !ok {
		return 0, fmt.Errorf("parse coverprofile line ref %q", ref)
	}
	parsed, err := strconv.Atoi(line)
	if err != nil {
		return 0, fmt.Errorf("parse coverprofile line %q: %w", line, err)
	}
	return parsed, nil
}
