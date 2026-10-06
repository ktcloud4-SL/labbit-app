package workspacefile

import (
	"errors"
	"strings"
	"testing"
)

func TestParseAcceptsCanonicalPaths(t *testing.T) {
	for _, raw := range []string{
		"a",
		"a/b",
		"src/main.py",
		"deep/er/and/deeper/file.txt",
		"한글/파일.txt",
		"emoji-😀/ok",
		"with space/a b.txt",
		".hidden",
		"dir/.hidden",
		"...",
		"a.b/c.d",
		// '%' 뒤에 16진수 두 개가 따르지 않으면 percent-escape가 아니다.
		"100%.txt",
		"%",
		"a%2",
		"%zz",
		"tab\u00a0nbsp", // U+00A0은 제어 문자가 아니다.
	} {
		file, err := ParseFile(raw)
		if err != nil || file.String() != raw {
			t.Errorf("ParseFile(%q) = %q, %v, want %q", raw, file.String(), err, raw)
		}
		dir, err := ParseDirectory(raw)
		if err != nil || dir.String() != raw || dir.IsRoot() {
			t.Errorf("ParseDirectory(%q) = %q, %v, want %q", raw, dir.String(), err, raw)
		}
	}
}

func TestParseDirectoryAcceptsOnlyTheEmptyStringAsRoot(t *testing.T) {
	root, err := ParseDirectory("")
	if err != nil || !root.IsRoot() || root.String() != "" {
		t.Fatalf("ParseDirectory(\"\") = %+v, %v, want root", root, err)
	}
	// 파일은 root가 될 수 없다.
	if _, err := ParseFile(""); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("ParseFile(\"\") error = %v, want ErrInvalidPath", err)
	}
	// root를 다른 방식으로 쓰는 값은 모두 거절한다.
	for _, raw := range []string{".", "/", "./", "//"} {
		if _, err := ParseDirectory(raw); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("ParseDirectory(%q) error = %v, want ErrInvalidPath", raw, err)
		}
	}
}

func TestParseRejectsUnsafeOrAmbiguousPaths(t *testing.T) {
	longSegment := strings.Repeat("a", maxSegmentBytes+1)
	longPath := strings.Repeat("a/", maxPathBytes/2) + "a"

	cases := []struct {
		name   string
		raw    string
		reason string
	}{
		{"absolute", "/etc/passwd", reasonAbsolute},
		{"absolute one segment", "/foo", reasonAbsolute},
		{"dot", ".", reasonDotSegment},
		{"dot-dot", "..", reasonDotSegment},
		{"parent traversal", "../x", reasonDotSegment},
		{"nested traversal", "a/../b", reasonDotSegment},
		{"trailing traversal", "a/..", reasonDotSegment},
		{"dot in the middle", "a/./b", reasonDotSegment},
		{"leading dot segment", "./a", reasonDotSegment},
		{"backslash traversal", `..\x`, reasonBackslash},
		{"backslash separator", `a\b`, reasonBackslash},
		{"windows drive", `C:\x`, reasonBackslash},
		{"NUL", "a\x00b", reasonControlCharacter},
		{"NUL only", "\x00", reasonControlCharacter},
		{"newline", "a\nb", reasonControlCharacter},
		{"tab", "a\tb", reasonControlCharacter},
		{"DEL", "a\x7fb", reasonControlCharacter},
		{"duplicate separator", "a//b", reasonEmptySegment},
		{"trailing separator", "a/", reasonTrailingSep},
		{"leading separator and trailing", "/a/", reasonAbsolute},
		{"invalid utf-8", "a/\xff\xfe", reasonInvalidUTF8},
		{"overlong segment", longSegment, reasonSegmentTooLong},
		{"overlong path", longPath, reasonTooLong},

		// HTTP layer가 query를 한 번 decode한 값이 어떻게든 percent-escape로 남아 있으면 거절한다. 이 package는 다시 decode하지 않는다.
		{"encoded dot-dot lower", "%2e%2e", reasonPercentEscape},
		{"encoded dot-dot upper", "%2E%2E", reasonPercentEscape},
		{"encoded dot-dot slash", "%2e%2e/x", reasonPercentEscape},
		{"encoded dot-dot after segment", "a/%2e%2e/b", reasonPercentEscape},
		{"mixed case hex", "%2e%2E", reasonPercentEscape},
		{"encoded slash lower", "a%2fb", reasonPercentEscape},
		{"encoded slash upper", "a%2Fb", reasonPercentEscape},
		{"encoded backslash lower", "a%5cb", reasonPercentEscape},
		{"encoded backslash upper", "a%5Cb", reasonPercentEscape},
		{"encoded leading slash", "%2fetc/passwd", reasonPercentEscape},
		{"double encoded dot-dot lower", "%252e%252e", reasonPercentEscape},
		{"double encoded dot-dot upper", "%252E%252E", reasonPercentEscape},
		{"double encoded slash", "a%252fb", reasonPercentEscape},
		{"double encoded backslash", "a%255cb", reasonPercentEscape},
		{"encoded NUL", "a%00b", reasonPercentEscape},
		{"encoded literal percent", "100%25", reasonPercentEscape},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, parse := range []struct {
				name string
				fn   func(string) (Path, error)
			}{{"ParseFile", ParseFile}, {"ParseDirectory", ParseDirectory}} {
				got, err := parse.fn(tc.raw)
				if !errors.Is(err, ErrInvalidPath) {
					t.Fatalf("%s(%q) = %q, %v, want ErrInvalidPath", parse.name, tc.raw, got.String(), err)
				}
				var pathErr *PathError
				if !errors.As(err, &pathErr) || pathErr.Reason != tc.reason {
					t.Fatalf("%s(%q) reason = %+v, want %q", parse.name, tc.raw, pathErr, tc.reason)
				}
				// 오류 문구는 경로 원문을 담지 않는다.
				if tc.raw != "" && strings.Contains(err.Error(), tc.raw) {
					t.Fatalf("%s(%q) error text contains the raw path: %q", parse.name, tc.raw, err)
				}
				if got.String() != "" {
					t.Fatalf("%s(%q) returned a usable path %q alongside an error", parse.name, tc.raw, got.String())
				}
			}
		})
	}
}

func TestParseNeverRewritesAcceptedInput(t *testing.T) {
	// 경로를 고쳐 쓰지 않는다. 받아들인 값은 입력과 byte 단위로 같다. NFC/NFD도 바꾸지 않는다.
	const nfd = "e\u0301.txt"
	got, err := ParseFile(nfd)
	if err != nil || got.String() != nfd {
		t.Fatalf("ParseFile(NFD) = %q, %v, want unchanged", got.String(), err)
	}
}

func TestChildComposesOnlyRepresentableNames(t *testing.T) {
	root, _ := ParseDirectory("")
	src, _ := ParseDirectory("src")

	for _, tc := range []struct {
		dir  Path
		name string
		want string
		ok   bool
	}{
		{root, "main.py", "main.py", true},
		{src, "main.py", "src/main.py", true},
		{src, "한글.txt", "src/한글.txt", true},
		{src, "100%.txt", "src/100%.txt", true},

		// 구분자를 포함한 이름은 한 segment가 아니므로 중첩 경로로 해석하지 않는다.
		{root, "a/b", "", false},
		{src, "a/b", "", false},
		{src, "", "", false},
		{src, ".", "", false},
		{src, "..", "", false},
		{root, `a\b`, "", false},
		{root, "a\nb", "", false},
		{root, "%2e%2e", "", false},
		{root, "a%2fb", "", false},
		{root, strings.Repeat("a", maxSegmentBytes+1), "", false},
	} {
		got, ok := tc.dir.child(tc.name)
		if ok != tc.ok || got.String() != tc.want {
			t.Errorf("%q.child(%q) = %q, %v, want %q, %v", tc.dir.String(), tc.name, got.String(), ok, tc.want, tc.ok)
		}
	}
}

func TestParseRevisionMatchesTheConnectorSchemaPattern(t *testing.T) {
	for _, raw := range []string{"a", "abc-DEF_123", strings.Repeat("a", 128), "0123456789abcdef"} {
		if _, ok := ParseRevision(raw); !ok {
			t.Errorf("ParseRevision(%q) rejected a revision the schema allows", raw)
		}
	}
	for _, raw := range []string{"", strings.Repeat("a", 129), `a"b`, "a b", "a/b", "a=b", "a+b", "\xff", "한글", "a\n"} {
		if _, ok := ParseRevision(raw); ok {
			t.Errorf("ParseRevision(%q) accepted a revision the schema rejects", raw)
		}
	}
}
