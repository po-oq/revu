package diff

import (
	"fmt"
	"strings"
	"testing"
)

func flatten(hunks []Hunk) []Line {
	var out []Line
	for _, hunk := range hunks {
		out = append(out, hunk.Lines...)
	}
	return out
}

func opTexts(lines []Line) string {
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		parts = append(parts, string(line.Op)+":"+line.Text)
	}
	return strings.Join(parts, "|")
}

func TestIdenticalTextsHaveNoHunks(t *testing.T) {
	result := Lines("alpha\nbeta", "alpha\nbeta")
	if result.TooLarge || len(result.Hunks) != 0 {
		t.Fatalf("result = %+v, want no hunks", result)
	}
}

func TestTrailingNewlineAndCRLFAreNormalized(t *testing.T) {
	result := Lines("alpha\r\nbeta\r\n", "alpha\nbeta")
	if result.TooLarge || len(result.Hunks) != 0 {
		t.Fatalf("result = %+v, want no hunks", result)
	}
}

func TestEmptyToContentIsAllAdds(t *testing.T) {
	result := Lines("", "alpha\nbeta")
	if len(result.Hunks) != 1 {
		t.Fatalf("hunks = %+v, want 1", result.Hunks)
	}
	if result.Hunks[0].OldStart != 1 || result.Hunks[0].NewStart != 1 {
		t.Fatalf("hunk starts = %+v, want 1/1", result.Hunks[0])
	}
	if got := opTexts(flatten(result.Hunks)); got != "add:alpha|add:beta" {
		t.Fatalf("lines = %s, want add:alpha|add:beta", got)
	}
}

func TestContentToEmptyIsAllDels(t *testing.T) {
	result := Lines("alpha\nbeta", "")
	if got := opTexts(flatten(result.Hunks)); got != "del:alpha|del:beta" {
		t.Fatalf("lines = %s, want del:alpha|del:beta", got)
	}
}

func TestSingleChangeKeepsThreeContextLines(t *testing.T) {
	oldLines := []string{"l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8", "l9"}
	newLines := append([]string{}, oldLines...)
	newLines[4] = "changed"
	result := Lines(strings.Join(oldLines, "\n"), strings.Join(newLines, "\n"))
	if len(result.Hunks) != 1 {
		t.Fatalf("hunks = %+v, want 1", result.Hunks)
	}
	hunk := result.Hunks[0]
	if hunk.OldStart != 2 || hunk.NewStart != 2 {
		t.Fatalf("hunk start = %d/%d, want 2/2", hunk.OldStart, hunk.NewStart)
	}
	want := "ctx:l2|ctx:l3|ctx:l4|del:l5|add:changed|ctx:l6|ctx:l7|ctx:l8"
	if got := opTexts(hunk.Lines); got != want {
		t.Fatalf("hunk lines = %s, want %s", got, want)
	}
}

func TestNearbyChangesShareOneHunk(t *testing.T) {
	oldLines := []string{"l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8"}
	newLines := append([]string{}, oldLines...)
	newLines[1] = "x2"
	newLines[5] = "x6"
	result := Lines(strings.Join(oldLines, "\n"), strings.Join(newLines, "\n"))
	if len(result.Hunks) != 1 {
		t.Fatalf("hunks = %+v, want 1 merged hunk", result.Hunks)
	}
}

func TestFarApartChangesProduceTwoHunks(t *testing.T) {
	oldLines := make([]string, 20)
	for i := range oldLines {
		oldLines[i] = fmt.Sprintf("l%d", i+1)
	}
	newLines := append([]string{}, oldLines...)
	newLines[1] = "x2"
	newLines[15] = "x16"
	result := Lines(strings.Join(oldLines, "\n"), strings.Join(newLines, "\n"))
	if len(result.Hunks) != 2 {
		t.Fatalf("hunks = %+v, want 2", result.Hunks)
	}
	if result.Hunks[0].OldStart != 1 || result.Hunks[1].OldStart != 13 {
		t.Fatalf("hunk starts = %d/%d, want 1/13", result.Hunks[0].OldStart, result.Hunks[1].OldStart)
	}
}

func TestTooLargeByLineCount(t *testing.T) {
	var builder strings.Builder
	for i := 0; i < maxLines+1; i++ {
		fmt.Fprintf(&builder, "line %d\n", i)
	}
	result := Lines(builder.String(), "")
	if !result.TooLarge || result.Hunks != nil {
		t.Fatalf("result tooLarge=%v hunks=%d, want tooLarge with no hunks", result.TooLarge, len(result.Hunks))
	}
}

func TestTooLargeByEditDistance(t *testing.T) {
	var oldBuilder, newBuilder strings.Builder
	for i := 0; i < maxEditDistance; i++ {
		fmt.Fprintf(&oldBuilder, "old %d\n", i)
		fmt.Fprintf(&newBuilder, "new %d\n", i)
	}
	result := Lines(oldBuilder.String(), newBuilder.String())
	if !result.TooLarge {
		t.Fatalf("result = %+v, want tooLarge by edit distance", result)
	}
}
