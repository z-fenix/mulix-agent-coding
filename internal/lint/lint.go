// Package lint 实现分层检查工具（M17 / design 002 §6）：
//
//  1. layer-import——分层导入矩阵（rules.go 内置 design 001 §5.3 分层表 +
//     design 002 修订）；行级 import 块扫描（P1 repolint regex 先例规模化）。
//  2. system-prompt-path / turn-tail——注入点唯一引用扫描（P1
//     TestNoSecondSystemPromptPath 规则并入）。
//  3. ratchet——文件职责棘轮（design 001 §5.3 第 5 条）：ratchet.json
//     以 map[file]int 记录每文件允许的违规计数；减/平 → 通过，增 → 违规
//     （Rule="ratchet"）。基线缺席 = 棘轮不启用（仍报原始违规）。
//
// 零新依赖：os/filepath/regexp/json——纯文本行扫描，不编译被检树。
// 产出：RunLayers（检查）/ GenerateRatchet（--fix 基线生成，只紧不松）。
package lint

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Violation 是一条分层违规（brief Task 10：报告 file:line 与规则名）。
type Violation struct {
	File   string // 仓库相对路径（正斜杠——Windows/CI 输出一致）
	Line   int    // 1 起行号；ratchet 汇总违规取该文件首条违规行
	Rule   string // layer-import | system-prompt-path | turn-tail | ratchet
	Detail string // 人读细节
}

// importRef 是扫描到的一条 import 声明（行级定位）。
type importRef struct {
	Path string
	Line int
}

var (
	// singleImportRe 匹配单行 import（含空白/下划线/点别名）。
	singleImportRe = regexp.MustCompile(`^\s*import\s+(?:(?:[.]|[_A-Za-z][_A-Za-z0-9]*)\s+)?"([^"]+)"`)
	// importBlockRe 匹配 import ( 块开始行。
	importBlockRe = regexp.MustCompile(`^\s*import\s*\(`)
	// quotedPathRe 从块内行提取导入路径。
	quotedPathRe = regexp.MustCompile(`"([^"]+)"`)
	// moduleRe 从 go.mod 提取模块名。
	moduleRe = regexp.MustCompile(`(?m)^\s*module\s+(\S+)`)
)

// RunLayers 对 repoRoot（Go 模块根）跑全部分层规则；ratchetPath 非空且文件
// 存在时启用文件职责棘轮（缺席 = 不启用；JSON 非法 = error）。返回的违规
// 按 File/Line/Rule 确定性排序；退出码由调用方按违规数决定（1 = 有违规）。
func RunLayers(repoRoot string, ratchetPath string) ([]Violation, error) {
	raw, err := scanRawViolations(repoRoot)
	if err != nil {
		return nil, err
	}
	baseline, active, err := loadRatchet(ratchetPath)
	if err != nil {
		return nil, err
	}
	return applyRatchet(raw, baseline, active), nil
}

// GenerateRatchet 重生成棘轮基线（--fix 模式；首轮人工生成入库）：跑原始
// 规则扫描，按文件计数写 ratchetPath。既有基线取 max 合并——棘轮只紧不松，
// 放松须经人工编辑 JSON（显式决定）。返回本次扫描的原始违规（调用方展示）。
func GenerateRatchet(repoRoot string, ratchetPath string) ([]Violation, error) {
	raw, err := scanRawViolations(repoRoot)
	if err != nil {
		return nil, err
	}
	current := map[string]int{}
	for _, v := range raw {
		current[v.File]++
	}
	old, _, err := loadRatchet(ratchetPath)
	if err != nil {
		return nil, err
	}
	merged := map[string]int{}
	for f, n := range old {
		merged[f] = n // 既有条目先入——file 消失也保留（只紧不松）
	}
	for f, n := range current {
		if n > merged[f] {
			merged[f] = n // max 合并：本次更少不放松既有记录
		}
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(ratchetPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(ratchetPath, append(data, '\n'), 0o644); err != nil {
		return nil, err
	}
	return raw, nil
}

// scanRawViolations 跑全部规则（不含棘轮调整）：walk 非 test 的 .go 文件，
// 逐文件做 import 矩阵检查与标记引用扫描。
func scanRawViolations(repoRoot string) ([]Violation, error) {
	module, err := moduleOf(repoRoot)
	if err != nil {
		return nil, err
	}
	var violations []Violation
	err = filepath.WalkDir(repoRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// P1 repolint 先例口径：隐藏目录（.git/.mulix/…）、bin、testdata 跳过。
			if p != repoRoot && (strings.HasPrefix(d.Name(), ".") || d.Name() == "bin" || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		vs, err := scanFile(module, rel, p)
		if err != nil {
			return err
		}
		violations = append(violations, vs...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortViolations(violations)
	return violations, nil
}

// scanFile 对单个 .go 文件做两项检查：import 矩阵 + 标记引用。
func scanFile(module, rel, abs string) ([]Violation, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	pkgPath := path.Dir(rel)
	if pkgPath == "." {
		pkgPath = "" // 模块根包（本仓库无根包代码；fixture 亦无需特判）
	}
	var violations []Violation
	imports, err := scanImports(lines)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	violations = append(violations, checkLayerImports(module, pkgPath, rel, imports)...)
	violations = append(violations, checkMarkers(rel, lines)...)
	return violations, nil
}

// scanImports 提取文件全部 import 声明（单行 + 块内逐行），带行号。
// 注释剥离（T10-①，008 FR-101）：块内先剥离注释再判定——行注释（// 至
// 行尾，引号外首个 //）与块注释（/* */，含跨行）不参与闭合判定与 import
// 提取；闭合 = 剥离后整行 TrimSpace 为 ")"。扫描结果与「剥离注释的等价
// 文件」一致——注释既不假闭合也不假 import。
func scanImports(lines []string) ([]importRef, error) {
	var refs []importRef
	inBlock := false
	inBlockComment := false
	for i, line := range lines {
		if inBlockComment {
			if idx := strings.Index(line, "*/"); idx >= 0 {
				line = line[idx+2:] // 块注释收尾:其余部分继续按代码处理
				inBlockComment = false
			} else {
				continue // 整行在块注释内
			}
		}
		if inBlock {
			code, wholeComment := splitLineComment(line)
			if wholeComment {
				if strings.HasPrefix(strings.TrimSpace(line), "/*") && !strings.Contains(line, "*/") {
					inBlockComment = true // /* 开启且本行无收尾:转多行状态
				}
				continue
			}
			if strings.TrimSpace(code) == ")" {
				inBlock = false
				continue
			}
			if m := quotedPathRe.FindStringSubmatch(code); m != nil {
				refs = append(refs, importRef{Path: m[1], Line: i + 1})
			}
			continue
		}
		if importBlockRe.MatchString(line) {
			inBlock = true
			continue
		}
		if m := singleImportRe.FindStringSubmatch(line); m != nil {
			refs = append(refs, importRef{Path: m[1], Line: i + 1})
		}
	}
	if inBlock {
		return nil, errors.New("import 块未闭合")
	}
	return refs, nil
}

// splitLineComment 把一行拆为代码部分与行尾注释:返回 code(引号外首个 //
// 或 /* 之前的部分)与 wholeComment(该行是否为整行注释——以 // 或 /*
// 起始)。引号内 // 不视为注释起始。
func splitLineComment(line string) (code string, wholeComment bool) {
	inQuote := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQuote = !inQuote
		case '/':
			if !inQuote && i+1 < len(line) && (line[i+1] == '/' || line[i+1] == '*') {
				trimmed := strings.TrimSpace(line[:i])
				if trimmed == "" {
					return "", true // 整行注释(// 或 /* 起始)
				}
				return line[:i], false // 行尾注释:代码部分为其前缀
			}
		}
	}
	return line, false
}

// checkLayerImports 把模块内 import 对照分层矩阵（模块外导入不设限）。
func checkLayerImports(module, pkgPath, rel string, imports []importRef) []Violation {
	var violations []Violation
	for _, imp := range imports {
		internalPkg, ok := strings.CutPrefix(imp.Path, module+"/")
		if !ok || internalPkg == "" {
			continue // 模块外依赖（stdlib/外部）与模块根包：不在矩阵管辖内
		}
		if pkgAllowed(pkgPath, internalPkg) {
			continue
		}
		var detail string
		if _, registered := allowFor(pkgPath); registered {
			detail = fmt.Sprintf("import %q 超出包 %s 的分层允许清单（design 001 §5.3）", imp.Path, pkgPath)
		} else {
			detail = fmt.Sprintf("import %q：包 %s 未登记于分层矩阵，按 leaf 处理（禁止内部导入；新包请登记 internal/lint/rules.go）", imp.Path, pkgPath)
		}
		violations = append(violations, Violation{File: rel, Line: imp.Line, Rule: "layer-import", Detail: detail})
	}
	return violations
}

// checkMarkers 行级扫描标记引用（allowlist 外的每一引用行一条违规；
// 注释与字符串同计——P1 TestNoSecondSystemPromptPath 的 strings.Contains 口径）。
func checkMarkers(rel string, lines []string) []Violation {
	var violations []Violation
	for _, rule := range refRules {
		if rule.Allow[rel] {
			continue
		}
		for i, line := range lines {
			if strings.Contains(line, rule.Marker) {
				violations = append(violations, Violation{File: rel, Line: i + 1, Rule: rule.Rule, Detail: rule.Reason})
			}
		}
	}
	return violations
}

// moduleOf 读 repoRoot/go.mod 的模块名（缺文件 = 不是模块根，报错提示）。
func moduleOf(repoRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%s 缺 go.mod——lint layers 需在 Go 模块根运行", repoRoot)
	}
	if err != nil {
		return "", err
	}
	m := moduleRe.FindSubmatch(data)
	if m == nil {
		return "", fmt.Errorf("%s/go.mod 无 module 声明", repoRoot)
	}
	return string(m[1]), nil
}

// loadRatchet 读棘轮基线：返回 (基线, 是否启用, error)。ratchetPath 为空或
// 文件缺席 → 不启用；JSON 非法 → error（fail-closed，不静默当空基线）。
func loadRatchet(ratchetPath string) (map[string]int, bool, error) {
	if ratchetPath == "" {
		return nil, false, nil
	}
	data, err := os.ReadFile(ratchetPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var baseline map[string]int
	if err := json.Unmarshal(data, &baseline); err != nil {
		return nil, false, fmt.Errorf("棘轮基线 %s 解析失败: %w", ratchetPath, err)
	}
	return baseline, true, nil
}

// applyRatchet 棘轮调整：基线内既有债务（文件计数 ≤ 允许值）不再报（减/平 →
// 通过）；突破基线（含基线未登记文件）→ 原始违规全量保留 + 一条 ratchet 违规。
func applyRatchet(raw []Violation, baseline map[string]int, active bool) []Violation {
	if !active {
		return raw
	}
	byFile := map[string][]Violation{}
	for _, v := range raw {
		byFile[v.File] = append(byFile[v.File], v)
	}
	var out []Violation
	for file, group := range byFile {
		allowed := baseline[file] // 缺席键 = 0：未登记文件的任何违规都是新增债务
		if _, ok := baseline[file]; ok && len(group) <= allowed {
			continue // 减/平 → 通过（记录债务允许，不重复报）
		}
		sortViolations(group)
		out = append(out, group...)
		out = append(out, Violation{
			File: file,
			Line: group[0].Line,
			Rule: "ratchet",
			Detail: fmt.Sprintf("文件违规 %d 项超过棘轮基线 %d（design 001 §5.3 第 5 条：编辑不得增加既有文件的记录债务）",
				len(group), allowed),
		})
	}
	sortViolations(out)
	return out
}

// sortViolations 按 File/Line/Rule 确定性排序（map 遍历序与 walk 序都需收敛）。
func sortViolations(vs []Violation) {
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].File != vs[j].File {
			return vs[i].File < vs[j].File
		}
		if vs[i].Line != vs[j].Line {
			return vs[i].Line < vs[j].Line
		}
		return vs[i].Rule < vs[j].Rule
	})
}
