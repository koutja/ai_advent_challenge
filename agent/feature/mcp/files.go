package mcpx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// filesRunner — обёртка над файловой системой репозитория для MCP-инструментов
// домена files. Корень репозитория резолвится через git rev-parse
// --show-toplevel; все пути проверяются на принадлежность репозиторию.
type filesRunner struct {
	mu   sync.Mutex
	root string
}

func (f *filesRunner) repoRoot(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.root != "" {
		return f.root, nil
	}
	out, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git недоступен или папка не репозиторий: %w", err)
	}
	f.root = strings.TrimSpace(string(out))
	return f.root, nil
}

// resolve приводит путь к абсолютному внутри репозитория и проверяет, что он
// не выходит за пределы корня (защита от ../).
func (f *filesRunner) resolve(ctx context.Context, p string) (string, error) {
	root, err := f.repoRoot(ctx)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(root, filepath.FromSlash(p))
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("путь %q вне репозитория", p)
	}
	return abs, nil
}

// --- Инструменты ---

// readFileOutput — результат read_file.
type readFileOutput struct {
	Path      string `json:"path"`
	Lines     int    `json:"lines"`
	Bytes     int    `json:"bytes"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

func (f *filesRunner) readFile(ctx context.Context, path string, maxLines int) (readFileOutput, error) {
	abs, err := f.resolve(ctx, path)
	if err != nil {
		return readFileOutput{}, err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return readFileOutput{}, err
	}
	content, truncated := limitLines(string(raw), maxLines)
	return readFileOutput{
		Path:      filepath.ToSlash(path),
		Lines:     strings.Count(string(raw), "\n") + 1,
		Bytes:     len(raw),
		Content:   content,
		Truncated: truncated,
	}, nil
}

// searchMatch — одно совпадение в файле.
type searchMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// searchFilesOutput — результат search_files.
type searchFilesOutput struct {
	Pattern string        `json:"pattern"`
	Total   int           `json:"total"`
	Matches []searchMatch `json:"matches"`
}

func (f *filesRunner) searchFiles(ctx context.Context, pattern, path, filePattern string, maxResults int) (searchFilesOutput, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return searchFilesOutput{}, fmt.Errorf("невалидный regexp: %w", err)
	}
	root, err := f.repoRoot(ctx)
	if err != nil {
		return searchFilesOutput{}, err
	}
	base := root
	if path != "" {
		base, err = f.resolve(ctx, path)
		if err != nil {
			return searchFilesOutput{}, err
		}
	}
	var fpRe *regexp.Regexp
	if filePattern != "" {
		fpRe, err = regexp.Compile(filePattern)
		if err != nil {
			return searchFilesOutput{}, fmt.Errorf("невалидный file_pattern: %w", err)
		}
	}

	res := searchFilesOutput{Pattern: pattern, Matches: []searchMatch{}}
	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != base && skipDir(p) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isTextFile(p) {
			return nil
		}
		if fpRe != nil && !fpRe.MatchString(filepath.Base(p)) {
			return nil
		}
		if maxResults > 0 && len(res.Matches) >= maxResults {
			return filepath.SkipAll
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		for i, line := range strings.Split(string(raw), "\n") {
			if re.MatchString(line) {
				res.Matches = append(res.Matches, searchMatch{
					File:    filepath.ToSlash(rel),
					Line:    i + 1,
					Content: truncateLine(line),
				})
				if maxResults > 0 && len(res.Matches) >= maxResults {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	res.Total = len(res.Matches)
	return res, nil
}

// listFilesOutput — результат list_files.
type listFilesOutput struct {
	Path  string   `json:"path"`
	Total int      `json:"total"`
	Files []string `json:"files"`
}

func (f *filesRunner) listFiles(ctx context.Context, path string, recursive bool) (listFilesOutput, error) {
	root, err := f.repoRoot(ctx)
	if err != nil {
		return listFilesOutput{}, err
	}
	base := root
	if path != "" {
		base, err = f.resolve(ctx, path)
		if err != nil {
			return listFilesOutput{}, err
		}
	}
	res := listFilesOutput{Path: path, Files: []string{}}
	if recursive {
		_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != base && skipDir(p) {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			res.Files = append(res.Files, filepath.ToSlash(rel))
			return nil
		})
	} else {
		entries, err := os.ReadDir(base)
		if err != nil {
			return res, err
		}
		for _, e := range entries {
			rel, _ := filepath.Rel(root, filepath.Join(base, e.Name()))
			res.Files = append(res.Files, filepath.ToSlash(rel))
		}
	}
	res.Total = len(res.Files)
	return res, nil
}

// writeFileOutput — результат write_file.
type writeFileOutput struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}

func (f *filesRunner) writeFile(ctx context.Context, path, content string) (writeFileOutput, error) {
	abs, err := f.resolve(ctx, path)
	if err != nil {
		return writeFileOutput{}, err
	}
	// Запрет на секреты, служебные файлы и пути-каталоги.
	base := filepath.Base(abs)
	if base == "" || base == "." || base == ".." || strings.HasSuffix(filepath.ToSlash(path), "/") {
		return writeFileOutput{}, fmt.Errorf("путь %q — не файл (каталог?)", path)
	}
	if strings.HasPrefix(base, ".env") || strings.Contains(filepath.ToSlash(abs), "/.git/") {
		return writeFileOutput{}, fmt.Errorf("запись в %q запрещена (секреты/служебные файлы)", path)
	}
	if dir := filepath.Dir(abs); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return writeFileOutput{}, err
		}
	}
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return writeFileOutput{}, err
	}
	if err := os.Rename(tmp, abs); err != nil {
		return writeFileOutput{}, err
	}
	return writeFileOutput{Path: filepath.ToSlash(path), Bytes: len(content)}, nil
}

// --- Регистрация инструментов ---

// addFilesTools регистрирует инструменты домена files (файловая система репозитория).
func addFilesTools(s *mcp.Server) {
	f := &filesRunner{}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "read_file",
		Description: "Читает файл репозитория (путь относительно корня) с лимитом строк.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in readFileInput) (*mcp.CallToolResult, readFileOutput, error) {
		res, err := f.readFile(ctx, in.Path, in.MaxLines)
		if err != nil {
			return nil, readFileOutput{}, err
		}
		return nil, res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_files",
		Description: "Ищет regexp по текстовым файлам репозитория (пропускает results/bin/.git/индекс) и возвращает файл:строка:совпадение.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchFilesInput) (*mcp.CallToolResult, searchFilesOutput, error) {
		res, err := f.searchFiles(ctx, in.Pattern, in.Path, in.FilePattern, in.MaxResults)
		if err != nil {
			return nil, searchFilesOutput{}, err
		}
		return nil, res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_files",
		Description: "Перечисляет файлы репозитория (рекурсивно или только каталог).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listFilesInput) (*mcp.CallToolResult, listFilesOutput, error) {
		res, err := f.listFiles(ctx, in.Path, in.Recursive)
		if err != nil {
			return nil, listFilesOutput{}, err
		}
		return nil, res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "write_file",
		Description: "Создаёт/перезаписывает файл в репозитории (атомарно; запрещены .env* и .git/).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in writeFileInput) (*mcp.CallToolResult, writeFileOutput, error) {
		res, err := f.writeFile(ctx, in.Path, in.Content)
		if err != nil {
			return nil, writeFileOutput{}, err
		}
		return nil, res, nil
	})
}

// --- Входные структуры (jsonschema-теги попадают в схему инструмента) ---

type readFileInput struct {
	// Path — путь относительно корня репозитория (например agent/agent.go).
	Path string `json:"path" jsonschema:"путь относительно корня репозитория, например agent/agent.go"`
	// MaxLines — лимит строк; 0 — без лимита.
	MaxLines int `json:"max_lines,omitempty" jsonschema:"лимит строк; 0 — без лимита"`
}

type searchFilesInput struct {
	// Pattern — регулярное выражение для поиска (обязательно).
	Pattern string `json:"pattern" jsonschema:"регулярное выражение для поиска (обязательное поле)"`
	// Path — ограничить поиск каталогом (пусто — весь репозиторий).
	Path string `json:"path,omitempty" jsonschema:"ограничить поиск каталогом, например agent; пусто — весь репозиторий"`
	// FilePattern — фильтр по имени файла (regexp); пусто — все текстовые файлы.
	FilePattern string `json:"file_pattern,omitempty" jsonschema:"фильтр по имени файла (regexp), например .*\\.go; пусто — все текстовые файлы"`
	// MaxResults — максимум совпадений; 0 — без лимита.
	MaxResults int `json:"max_results,omitempty" jsonschema:"максимум совпадений; 0 — без лимита"`
}

type listFilesInput struct {
	// Path — каталог относительно корня (пусто — корень).
	Path string `json:"path,omitempty" jsonschema:"каталог относительно корня; пусто — корень"`
	// Recursive — рекурсивный обход; по умолчанию false (только каталог).
	Recursive bool `json:"recursive,omitempty" jsonschema:"рекурсивный обход; по умолчанию false"`
}

type writeFileInput struct {
	// Path — путь относительно корня репозитория (например agent/docs/changelog.md).
	Path string `json:"path" jsonschema:"путь относительно корня репозитория, например agent/docs/changelog.md"`
	// Content — содержимое файла.
	Content string `json:"content" jsonschema:"содержимое файла"`
}

// --- Вспомогательные функции ---

// skipDir — каталоги, которые не обходим (генерируемые/служебные).
func skipDir(p string) bool {
	base := filepath.Base(p)
	switch base {
	case ".git", "results", "bin", "logs", "index", ".venv", ".codeassistant",
		"node_modules", ".cache", "__pycache__", ".pytest_cache", ".idea", ".vscode":
		return true
	}
	return false
}

// binaryExts — расширения, которые не считаем текстовыми.
var binaryExts = map[string]bool{
	".faiss": true, ".npy": true, ".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".webm": true, ".mp4": true, ".db": true, ".sqlite": true,
	".cast": true, ".pdf": true, ".woff": true, ".woff2": true, ".ttf": true,
	".ico": true, ".zip": true, ".gz": true,
}

// isTextFile — файл считается текстовым, если расширение не бинарное и в
// первых 8 КБ нет NUL-байтов.
func isTextFile(p string) bool {
	if binaryExts[strings.ToLower(filepath.Ext(p))] {
		return false
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	if len(raw) > 8192 {
		raw = raw[:8192]
	}
	return !bytes.Contains(raw, []byte{0})
}

// truncateLine обрезает строку совпадения до 200 символов.
func truncateLine(s string) string {
	const max = 200
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
