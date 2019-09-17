package git

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/go-errors/errors"

	"github.com/jesseduffield/lazygit/pkg/i18n"
	"github.com/jesseduffield/lazygit/pkg/utils"
	"github.com/sirupsen/logrus"
)

type PatchModifier struct {
	Log *logrus.Entry
	Tr  *i18n.Localizer
}

// NewPatchModifier builds a new branch list builder
func NewPatchModifier(log *logrus.Entry) (*PatchModifier, error) {
	return &PatchModifier{
		Log: log,
	}, nil
}

// ObtainPatchForHunk takes the original patch, which may contain several hunks,
// and removes any hunks that aren't the selected hunk
func (p *PatchModifier) ObtainPatchForHunk(patch string, hunkStarts []int, currentLine int) (string, error) {
	// get hunk start and end
	lines := strings.Split(patch, "\n")
	hunkStartIndex := utils.PrevIndex(hunkStarts, currentLine)
	hunkStart := hunkStarts[hunkStartIndex]
	nextHunkStartIndex := utils.NextIndex(hunkStarts, currentLine)
	var hunkEnd int
	if nextHunkStartIndex == 0 {
		hunkEnd = len(lines) - 1
	} else {
		hunkEnd = hunkStarts[nextHunkStartIndex]
	}

	headerLength, err := p.getHeaderLength(lines)
	if err != nil {
		return "", err
	}

	output := strings.Join(lines[0:headerLength], "\n") + "\n"
	output += strings.Join(lines[hunkStart:hunkEnd], "\n") + "\n"

	return output, nil
}

func (p *PatchModifier) getHeaderLength(patchLines []string) (int, error) {
	for index, line := range patchLines {
		if strings.HasPrefix(line, "@@") {
			return index, nil
		}
	}
	return 0, errors.New(p.Tr.SLocalize("CantFindHunks"))
}

// ObtainPatchForLine takes the original patch, which may contain several hunks,
// and the line number of the line we want to stage, and returns a new patch which only adds the selected line
func (p *PatchModifier) ObtainPatchForLine(patch string, lineNumber int) (string, error) {
	lines := strings.Split(patch, "\n")
	headerLength, err := p.getHeaderLength(lines)
	if err != nil {
		return "", err
	}
	output := strings.Join(lines[0:headerLength], "\n") + "\n"

	hunkStart, err := p.getHunkStart(lines, lineNumber)
	if err != nil {
		return "", err
	}

	hunk, err := p.getModifiedHunkForSingleLine(lines, hunkStart, lineNumber)
	if err != nil {
		return "", err
	}

	output += strings.Join(hunk, "\n")

	return output, nil
}

// ObtainPatchForHunkWithoutLine takes the original patch, which may contain several hunks,
// and the line number of the line we care about, and returns a patch containing the hunk without that line.
func (p *PatchModifier) ObtainPatchForHunkWithoutLine(patch string, lineNumber int) (string, error) {
	lines := strings.Split(patch, "\n")
	headerLength, err := p.getHeaderLength(lines)
	if err != nil {
		return "", err
	}
	output := strings.Join(lines[0:headerLength], "\n") + "\n"

	hunkStart, err := p.getHunkStart(lines, lineNumber)
	if err != nil {
		return "", err
	}

	hunk, err := p.getModifiedHunkForRemovedLine(lines, hunkStart, lineNumber)
	if err != nil {
		return "", err
	}

	output += strings.Join(hunk, "\n")

	return output, nil
}

// getHunkStart returns the line number of the hunk we're going to be modifying
// in order to stage our line
func (p *PatchModifier) getHunkStart(patchLines []string, lineNumber int) (int, error) {
	// find the hunk that we're modifying
	hunkStart := 0
	for index, line := range patchLines {
		if strings.HasPrefix(line, "@@") {
			hunkStart = index
		}
		if index == lineNumber {
			return hunkStart, nil
		}
	}

	return 0, errors.New(p.Tr.SLocalize("CantFindHunk"))
}

func (p *PatchModifier) getModifiedHunkForSingleLine(patchLines []string, hunkStart int, lineNumber int) ([]string, error) {
	lineChanges := 0
	// strip the hunk down to just the line we want to stage
	newHunk := []string{patchLines[hunkStart]}
	for offsetIndex, line := range patchLines[hunkStart+1:] {
		index := offsetIndex + hunkStart + 1
		if strings.HasPrefix(line, "@@") {
			newHunk = append(newHunk, "\n")
			break
		}
		if index != lineNumber {
			// we include other removals but treat them like context
			if strings.HasPrefix(line, "-") {
				newHunk = append(newHunk, " "+line[1:])
				lineChanges++
				continue
			}
			// we don't include other additions
			if strings.HasPrefix(line, "+") {
				lineChanges--
				continue
			}
		}
		newHunk = append(newHunk, line)
	}

	var err error
	newHunk[0], err = p.updatedHeader(newHunk[0], lineChanges)
	if err != nil {
		return nil, err
	}

	return newHunk, nil
}

// this returns a patch containing all lines in a patch but the selected one
func (p *PatchModifier) getModifiedHunkForRemovedLine(patchLines []string, hunkStart int, lineNumber int) ([]string, error) {
	lineChanges := 0
	// strip the hunk down to just the line we want to stage
	newHunk := []string{patchLines[hunkStart]}
	lastRemovedIndex := -1
	for offsetIndex, line := range patchLines[hunkStart+1:] {
		index := offsetIndex + hunkStart + 1
		if strings.HasPrefix(line, "@@") {
			newHunk = append(newHunk, "\n")
			break
		}
		if index == lineNumber {
			// we include other removals but treat them like context
			if strings.HasPrefix(line, "-") {
				lastRemovedIndex = index
				newHunk = append(newHunk, " "+line[1:])
				lineChanges++
				continue
			}
			// we don't include other additions
			if strings.HasPrefix(line, "+") {
				lineChanges--
				continue
			}
		} else {
			if strings.HasPrefix(line, `\`) && lastRemovedIndex == index-1 {
				// do nothing
			}
		}
		newHunk = append(newHunk, line)
	}

	var err error
	newHunk[0], err = p.updatedHeader(newHunk[0], lineChanges)
	if err != nil {
		return nil, err
	}

	return newHunk, nil
}

// after making changes to a hunk, sometimes it ends up empty. This tells us whether a hunk has any changes
func (p *PatchModifier) HunkPatchIsEmpty(patch string) bool {
	pastHeader := false
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@") {
			pastHeader = true
			continue
		}
		if pastHeader && (strings.HasPrefix(line, "-") || strings.HasPrefix(line, "+")) {
			return false
		}
	}
	return true
}

// updatedHeader returns the hunk header with the updated line range
// we need to update the hunk length to reflect the changes we made
// if the hunk has three additions but we're only staging one, then
// @@ -14,8 +14,11 @@ import (
// becomes
// @@ -14,8 +14,9 @@ import (
func (p *PatchModifier) updatedHeader(currentHeader string, lineChanges int) (string, error) {
	// current counter is the number after the second comma
	re := regexp.MustCompile(`(\d+) @@`)
	prevLengthString := re.FindStringSubmatch(currentHeader)[1]

	prevLength, err := strconv.Atoi(prevLengthString)
	if err != nil {
		return "", err
	}
	re = regexp.MustCompile(`\d+ @@`)
	newLength := strconv.Itoa(prevLength + lineChanges)
	return re.ReplaceAllString(currentHeader, newLength+" @@"), nil
}
