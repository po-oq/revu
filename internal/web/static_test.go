package web

import (
	"regexp"
	"strings"
	"testing"
)

func TestAppOwnedRuntimeAssetsUseNoExternalReferences(t *testing.T) {
	for _, path := range []string{
		"static/index.html",
		"static/styles.css",
		"static/app.js",
	} {
		data, err := Files.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", path, err)
		}
		source := string(data)
		for _, disallowed := range []string{
			"https://",
			"http://",
			"fonts.googleapis.com",
			"fonts.gstatic.com",
			"cdn.jsdelivr.net",
			"IBM Plex",
		} {
			if strings.Contains(source, disallowed) {
				t.Fatalf("%s contains external runtime reference %q", path, disallowed)
			}
		}
	}
}

func TestIndexUsesLocalRuntimeAssetTags(t *testing.T) {
	data, err := Files.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("ReadFile index.html: %v", err)
	}
	index := string(data)
	for _, expected := range []string{
		`src="/vendor/marked.min.js"`,
		`src="/vendor/purify.min.js"`,
		`src="/vendor/mermaid.min.js"`,
		`src="/vendor/svg-pan-zoom.min.js"`,
		`href="/styles.css"`,
		`src="/app.js"`,
	} {
		if !strings.Contains(index, expected) {
			t.Fatalf("index.html missing local asset reference %s", expected)
		}
	}
}

func TestVendoredBrowserLibrariesAreEmbedded(t *testing.T) {
	for _, path := range []string{
		"static/vendor/marked.min.js",
		"static/vendor/purify.min.js",
		"static/vendor/mermaid.min.js",
		"static/vendor/svg-pan-zoom.min.js",
		"static/vendor/THIRD_PARTY.md",
	} {
		data, err := Files.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", path, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s is empty", path)
		}
	}
	notice, err := Files.ReadFile("static/vendor/THIRD_PARTY.md")
	if err != nil {
		t.Fatalf("ReadFile THIRD_PARTY.md: %v", err)
	}
	for _, expected := range []string{
		"marked 18.0.5",
		"DOMPurify 3.4.11",
		"Mermaid 11.16.0",
		"svg-pan-zoom 3.6.2",
	} {
		if !strings.Contains(string(notice), expected) {
			t.Fatalf("THIRD_PARTY.md missing %q", expected)
		}
	}
}

func TestStylesUseSystemFonts(t *testing.T) {
	data, err := Files.ReadFile("static/styles.css")
	if err != nil {
		t.Fatalf("ReadFile styles.css: %v", err)
	}
	styles := string(data)
	for _, disallowed := range []string{"IBM Plex", "fonts.googleapis.com", "@font-face"} {
		if strings.Contains(styles, disallowed) {
			t.Fatalf("styles.css contains web font dependency %q", disallowed)
		}
	}
	for _, expected := range []string{
		`"Segoe UI"`,
		`"Yu Gothic UI"`,
		`"Hiragino Sans"`,
		`Meiryo`,
		`Consolas`,
		`"Cascadia Mono"`,
	} {
		if !strings.Contains(styles, expected) {
			t.Fatalf("styles.css missing system font %s", expected)
		}
	}
}

func TestHTMLPreviewSandboxBoundary(t *testing.T) {
	data, err := Files.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("ReadFile app.js: %v", err)
	}
	app := string(data)
	matches := regexp.MustCompile(`sandbox="([^"]*)"`).FindStringSubmatch(app)
	if len(matches) != 2 {
		t.Fatalf("HTML preview iframe sandbox attribute not found")
	}
	sandbox := matches[1]
	for _, required := range []string{"allow-scripts", "allow-forms", "allow-popups"} {
		if !strings.Contains(sandbox, required) {
			t.Fatalf("sandbox missing %s in %q", required, sandbox)
		}
	}
	if strings.Contains(sandbox, "allow-same-origin") {
		t.Fatalf("sandbox must not include allow-same-origin: %q", sandbox)
	}
}

func TestAppWiresHostDeleteSupport(t *testing.T) {
	data, err := Files.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("ReadFile app.js: %v", err)
	}
	app := string(data)
	for _, expected := range []string{
		`apiJson("/me")`,
		"state.isHost",
		"function canDeleteItem",
		"ホスト権限で削除します",
	} {
		if !strings.Contains(app, expected) {
			t.Fatalf("app.js missing host delete wiring %q", expected)
		}
	}
}

func TestAppWiresEditHistoryView(t *testing.T) {
	appData, err := Files.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("ReadFile app.js: %v", err)
	}
	app := string(appData)
	for _, expected := range []string{
		`state.view === "history"`,
		`data-action="open-history"`,
		"function renderHistoryView",
		"/edits/",
		"記録された編集履歴はありません",
		"差分が大きすぎるため全文を表示します",
		"thread.updatedAt !== thread.createdAt",
	} {
		if !strings.Contains(app, expected) {
			t.Fatalf("app.js missing edit history wiring %q", expected)
		}
	}
	stylesData, err := Files.ReadFile("static/styles.css")
	if err != nil {
		t.Fatalf("ReadFile styles.css: %v", err)
	}
	styles := string(stylesData)
	for _, expected := range []string{
		".history-shell",
		`.diff-line[data-op="add"]`,
		`.diff-line[data-op="del"]`,
	} {
		if !strings.Contains(styles, expected) {
			t.Fatalf("styles.css missing edit history style %q", expected)
		}
	}
}

func TestMermaidSVGIsNotSanitizedAfterRender(t *testing.T) {
	data, err := Files.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("ReadFile app.js: %v", err)
	}
	app := string(data)
	if strings.Contains(app, "DOMPurify.sanitize(result.svg)") {
		t.Fatalf("Mermaid render SVG must not be sanitized after render; it removes node labels from some diagrams")
	}
	if !strings.Contains(app, `securityLevel: "strict"`) {
		t.Fatalf("Mermaid SVG is inserted directly, so Mermaid securityLevel must remain strict")
	}
}
