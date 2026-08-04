package unidiff

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pmezard/go-difflib/difflib"

	"upskill/internal/fsutil"
)

const (
	defaultContext      = 3
	defaultMaxTextBytes = 1 << 20
)

// Options controls directory comparison and output bounds.
type Options struct {
	Context        int
	MaxTextBytes   int
	BeforeOptional bool
	AfterOptional  bool
	Ignore         func(path string) bool
}

// CompareDirs returns a deterministic unified diff for two directory trees.
// Each returned string is exactly one output line and never contains a newline.
func CompareDirs(beforeRoot string, afterRoot string, options Options) ([]string, error) {
	beforeFiles, err := readFiles(beforeRoot, options.BeforeOptional, options.Ignore)
	if err != nil {
		return nil, err
	}
	afterFiles, err := readFiles(afterRoot, options.AfterOptional, options.Ignore)
	if err != nil {
		return nil, err
	}

	context := options.Context
	if context <= 0 {
		context = defaultContext
	}
	maxTextBytes := options.MaxTextBytes
	if maxTextBytes <= 0 {
		maxTextBytes = defaultMaxTextBytes
	}

	paths := make(map[string]struct{}, len(beforeFiles)+len(afterFiles))
	for path := range beforeFiles {
		paths[path] = struct{}{}
	}
	for path := range afterFiles {
		paths[path] = struct{}{}
	}
	sortedPaths := make([]string, 0, len(paths))
	for path := range paths {
		sortedPaths = append(sortedPaths, path)
	}
	sort.Strings(sortedPaths)

	var output []string
	for _, path := range sortedPaths {
		before, hadBefore := beforeFiles[path]
		after, hasAfter := afterFiles[path]
		if hadBefore && hasAfter && bytes.Equal(before, after) {
			continue
		}
		output = append(output, fileDiff(path, before, hadBefore, after, hasAfter, context, maxTextBytes)...)
	}
	return output, nil
}

func readFiles(root string, optional bool, ignore func(path string) bool) (map[string][]byte, error) {
	files := map[string][]byte{}
	if root == "" {
		if optional {
			return files, nil
		}
		return nil, fmt.Errorf("diff directory is required")
	}
	if _, err := os.Stat(root); err != nil {
		if optional && os.IsNotExist(err) {
			return files, nil
		}
		return nil, err
	}
	paths, err := fsutil.WalkFiles(root)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		relative = filepath.ToSlash(relative)
		if ignore != nil && ignore(relative) {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		files[relative] = content
	}
	return files, nil
}

func fileDiff(
	path string,
	before []byte,
	hadBefore bool,
	after []byte,
	hasAfter bool,
	context int,
	maxTextBytes int,
) []string {
	beforePath := "a/" + path
	afterPath := "b/" + path
	fromFile := beforePath
	if !hadBefore {
		fromFile = "/dev/null"
	}
	toFile := afterPath
	if !hasAfter {
		toFile = "/dev/null"
	}

	output := []string{"diff --git " + beforePath + " " + afterPath}
	if isBinary(before) || isBinary(after) {
		return append(output, "Binary files "+fromFile+" and "+toFile+" differ")
	}
	if len(before) > maxTextBytes || len(after) > maxTextBytes {
		return append(output, fmt.Sprintf(
			"Files %s and %s differ (content diff omitted: exceeds %d bytes)",
			fromFile,
			toFile,
			maxTextBytes,
		))
	}

	output = append(output, "--- "+fromFile, "+++ "+toFile)
	return append(output, textDiff(before, after, context)...)
}

func isBinary(content []byte) bool {
	return bytes.IndexByte(content, 0) >= 0 || !utf8.Valid(content)
}

type textLine struct {
	text       string
	key        string
	terminated bool
}

func textDiff(before []byte, after []byte, context int) []string {
	beforeLines := splitLines(before)
	afterLines := splitLines(after)
	beforeKeys := lineKeys(beforeLines)
	afterKeys := lineKeys(afterLines)
	matcher := difflib.NewMatcherWithJunk(beforeKeys, afterKeys, false, nil)

	var output []string
	for _, group := range matcher.GetGroupedOpCodes(context) {
		first := group[0]
		last := group[len(group)-1]
		output = append(output, fmt.Sprintf(
			"@@ -%s +%s @@",
			formatRange(first.I1, last.I2),
			formatRange(first.J1, last.J2),
		))
		for _, operation := range group {
			switch operation.Tag {
			case 'e':
				output = appendPrefixedLines(output, " ", beforeLines[operation.I1:operation.I2])
			case 'r':
				output = appendPrefixedLines(output, "-", beforeLines[operation.I1:operation.I2])
				output = appendPrefixedLines(output, "+", afterLines[operation.J1:operation.J2])
			case 'd':
				output = appendPrefixedLines(output, "-", beforeLines[operation.I1:operation.I2])
			case 'i':
				output = appendPrefixedLines(output, "+", afterLines[operation.J1:operation.J2])
			}
		}
	}
	return output
}

func splitLines(content []byte) []textLine {
	if len(content) == 0 {
		return nil
	}
	parts := strings.SplitAfter(string(content), "\n")
	lines := make([]textLine, 0, len(parts))
	for index, part := range parts {
		if part == "" && index == len(parts)-1 {
			continue
		}
		terminated := strings.HasSuffix(part, "\n")
		text := strings.TrimSuffix(part, "\n")
		key := text + "\x00"
		if terminated {
			key = text + "\n"
		}
		lines = append(lines, textLine{text: text, key: key, terminated: terminated})
	}
	return lines
}

func lineKeys(lines []textLine) []string {
	keys := make([]string, len(lines))
	for index, line := range lines {
		keys[index] = line.key
	}
	return keys
}

func appendPrefixedLines(output []string, prefix string, lines []textLine) []string {
	for _, line := range lines {
		output = append(output, prefix+line.text)
		if !line.terminated {
			output = append(output, `\ No newline at end of file`)
		}
	}
	return output
}

func formatRange(start int, stop int) string {
	beginning := start + 1
	length := stop - start
	if length == 0 {
		beginning--
	}
	if length == 1 {
		return strconv.Itoa(beginning)
	}
	return fmt.Sprintf("%d,%d", beginning, length)
}
