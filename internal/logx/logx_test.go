package logx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initTemp points the logger at a temporary directory.
func initTemp(t *testing.T, opts Options) string {
	t.Helper()

	dir := t.TempDir()
	opts.Dir = dir
	if err := Init(opts); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(Close)
	return dir
}

// readLog reads the log file.
func readLog(t *testing.T, dir string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, "cdx.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return string(data)
}

// TestLevelFiltering checks filtering by level.
func TestLevelFiltering(t *testing.T) {
	dir := initTemp(t, Options{Level: LevelWarn})

	Debug("скрытое")
	Info("тоже скрытое")
	Warn("предупреждение")
	Error("ошибка")

	content := readLog(t, dir)
	if strings.Contains(content, "скрытое") {
		t.Fatal("debug must be filtered out")
	}
	if !strings.Contains(content, "предупреждение") || !strings.Contains(content, "ошибка") {
		t.Fatalf("warn and error must be written: %s", content)
	}
}

// TestFormatting checks argument substitution.
func TestFormatting(t *testing.T) {
	dir := initTemp(t, Options{Level: LevelDebug})

	Info("value is %d and %s", 42, "text")

	content := readLog(t, dir)
	if !strings.Contains(content, "value is 42 and text") {
		t.Fatalf("arguments must be formatted: %s", content)
	}
	if !strings.Contains(content, "info") {
		t.Fatalf("level must be present: %s", content)
	}
}

// TestSetLevel checks changing the level at runtime.
func TestSetLevel(t *testing.T) {
	dir := initTemp(t, Options{Level: LevelError})

	Debug("до смены")
	SetLevel(LevelDebug)
	Debug("после смены")

	content := readLog(t, dir)
	if strings.Contains(content, "до смены") {
		t.Fatal("message before level change must be filtered")
	}
	if !strings.Contains(content, "после смены") {
		t.Fatalf("message after level change must be written: %s", content)
	}
}

// TestRotationBySize checks rotation by size.
func TestRotationBySize(t *testing.T) {
	dir := initTemp(t, Options{Level: LevelDebug, MaxSize: 200, MaxFiles: 3})

	for i := 0; i < 50; i++ {
		Info("строка номер %d для заполнения журнала", i)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	archives := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "cdx.log.") {
			archives++
		}
	}
	if archives == 0 {
		t.Fatal("expected at least one rotated archive")
	}
	if archives > 3 {
		t.Fatalf("archive count %d exceeds max files", archives)
	}
}

// TestParseLevel checks parsing a level.
func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"debug":   LevelDebug,
		"info":    LevelInfo,
		"":        LevelInfo,
		"warn":    LevelWarn,
		"warning": LevelWarn,
		"error":   LevelError,
		"ERROR":   LevelError,
	}
	for input, want := range cases {
		got, err := ParseLevel(input)
		if err != nil {
			t.Fatalf("ParseLevel(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseLevel(%q) = %v, want %v", input, got, want)
		}
	}

	if _, err := ParseLevel("nope"); err == nil {
		t.Fatal("expected error for unknown level")
	}
}

// TestLevelString checks the text names of the levels.
func TestLevelString(t *testing.T) {
	cases := map[Level]string{
		LevelDebug: "debug",
		LevelInfo:  "info",
		LevelWarn:  "warn",
		LevelError: "error",
		Level(99):  "unknown",
	}
	for level, want := range cases {
		if got := level.String(); got != want {
			t.Fatalf("String() = %q, want %q", got, want)
		}
	}
}

// TestInitReturnsErrorForBadDir checks the error for an unusable directory.
func TestInitReturnsErrorForBadDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := Init(Options{Dir: filepath.Join(file, "logs")}); err == nil {
		t.Fatal("expected error for path under a file")
	}
}

// TestPanicWritesStack checks that a panic is written.
func TestPanicWritesStack(t *testing.T) {
	dir := initTemp(t, Options{Level: LevelError})

	Panic("сбой обработчика", "boom")

	content := readLog(t, dir)
	if !strings.Contains(content, "сбой обработчика") || !strings.Contains(content, "boom") {
		t.Fatalf("panic must be written: %s", content)
	}
}

// TestNoDirMeansStderr checks the mode without a file.
func TestNoDirMeansStderr(t *testing.T) {
	if err := Init(Options{Level: LevelInfo}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(Close)

	// No error is expected: the message goes to stderr.
	Info("без файла")
}

// TestRotationKeepsRecent checks that old archives are removed.
func TestRotationKeepsRecent(t *testing.T) {
	dir := initTemp(t, Options{Level: LevelDebug, MaxSize: 120, MaxFiles: 1})

	for i := 0; i < 40; i++ {
		Info("запись %d с достаточным объёмом текста", i)
	}
	// Let rotation happen.
	time.Sleep(10 * time.Millisecond)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	archives := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "cdx.log.") {
			archives++
		}
	}
	if archives > 1 {
		t.Fatalf("expected at most one archive, got %d", archives)
	}
}
