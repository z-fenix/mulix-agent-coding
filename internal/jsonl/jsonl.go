// Package jsonl 提供 JSONL 追加日志：追加写（每行一 JSON，fsync 可选）、
// 增量读与损坏尾部检测（返回已完整行数与 damaged 标记）。sessionx 直接依赖。
// 红线：损坏尾部不得静默丢弃——读侧显式标记，写侧拒绝在损坏尾部上追加（需显式 Repair）。
// 本文件是包的权威文档；doc.go 仅保留短句。
package jsonl

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
)

// Options 控制写入行为。
type Options struct {
	// Fsync 为 true 时每次 Append 后调用 Sync（崩溃安全优先；性能敏感路径关闭）。
	Fsync bool
}

// Writer 追加写 JSONL 文件。每行一个完整 JSON（调用方保证字节为单行 JSON；
// 本包负责换行与磁盘落盘策略）。
type Writer struct {
	f     *os.File
	fsync bool
	// damagedTail 记录打开时检测到的损坏尾部；非空时 Append 拒绝写入。
	damagedTail bool
}

// OpenWriter 以默认选项（无 fsync）打开追加写。
func OpenWriter(path string) (*Writer, error) {
	return OpenWriterWithOptions(path, Options{})
}

// OpenWriterWithOptions 打开追加写并应用选项；打开时检测损坏尾部。
func OpenWriterWithOptions(path string, opts Options) (*Writer, error) {
	// Windows 上以只写句柄无法 ReadAt：先用只读句柄检测损坏尾部，再开追加句柄。
	// 检测失败 fail-closed：视为损坏尾部（Append 拒绝写入，Repair 后可继续），
	// 绝不静默当作干净尾部——否则新行会熔进半行，损坏整条记录流。
	damaged := false
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		probe, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("jsonl: open probe: %w", err)
		}
		buf := make([]byte, 1)
		if _, err := probe.ReadAt(buf, info.Size()-1); err != nil {
			probe.Close()
			return nil, fmt.Errorf("jsonl: read last byte: %w", err)
		}
		damaged = buf[0] != '\n'
		probe.Close()
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("jsonl: open writer: %w", err)
	}
	w := &Writer{f: f, fsync: opts.Fsync, damagedTail: damaged}
	return w, nil
}

// Append 追加一行（b 应为不含换行的完整 JSON 字节）。文件不存在时创建。
// 任一写入失败后本 writer 进入损坏态（damagedTail=true）：半行已可能落盘，
// 后续 Append 一律拒绝，直至显式 Repair——防止记录流被静默熔接。
func (w *Writer) Append(b []byte) error {
	if w.damagedTail {
		return fmt.Errorf("jsonl: refuse to append onto damaged tail (call Repair first)")
	}
	if len(b) == 0 {
		return fmt.Errorf("jsonl: empty record")
	}
	if bytes.ContainsAny(b, "\r\n") {
		return fmt.Errorf("jsonl: record must be a single line")
	}
	line := make([]byte, 0, len(b)+1)
	line = append(line, b...)
	line = append(line, '\n')
	if _, err := w.f.Write(line); err != nil {
		w.damagedTail = true
		return fmt.Errorf("jsonl: write: %w", err)
	}
	if w.fsync {
		if err := w.f.Sync(); err != nil {
			w.damagedTail = true
			return fmt.Errorf("jsonl: sync: %w", err)
		}
	}
	return nil
}

// Sync 强制落盘（per-kind fsync 的显式入口：审批等安全关键事件由调用方
// 按 kind 选择性调用；全量 fsync 用 Options.Fsync）。sync 失败 = 持久化
// 完整性未知，调用方必须按写失败处理（fail-closed），不得照常广播。
func (w *Writer) Sync() error {
	if err := w.f.Sync(); err != nil {
		w.damagedTail = true
		return fmt.Errorf("jsonl: sync: %w", err)
	}
	return nil
}

// Repair 截断损坏尾部（保留此前全部完整行），使写入可继续。
// 以**实际文件状态**为准：不信任缓存的 damagedTail 标志（打开后文件可能被
// 带外改动——如恢复流程再次撞上损坏），每次都重新探测末字节。
// 幂等：末字节为换行（无损坏）时为 no-op。
func (w *Writer) Repair() error {
	path := w.f.Name()
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("jsonl: stat: %w", err)
	}
	// 重新探测实际末字节，而不是信任缓存的 damagedTail（Task 10 评审裁决）。
	probe0, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("jsonl: open probe: %w", err)
	}
	var last [1]byte
	if _, err := probe0.ReadAt(last[:], info.Size()-1); err != nil {
		probe0.Close()
		return fmt.Errorf("jsonl: read last byte: %w", err)
	}
	probe0.Close()
	if last[0] == '\n' {
		w.damagedTail = false
		return nil // 无损坏尾部：no-op（幂等）
	}
	w.damagedTail = true
	// 从文件末尾向前找最后一个换行；截断到其后（用只读句柄扫描，避开 Windows 只写句柄限制）。
	size := info.Size()
	probe, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("jsonl: open probe: %w", err)
	}
	cut := int64(0)
	buf := make([]byte, 4096)
	read := int64(0)
	for read < size {
		chunk := int64(len(buf))
		if read+chunk > size {
			chunk = size - read
		}
		start := size - read - chunk
		if _, err := probe.ReadAt(buf[:chunk], start); err != nil {
			probe.Close()
			return fmt.Errorf("jsonl: read at: %w", err)
		}
		if idx := bytes.LastIndexByte(buf[:chunk], '\n'); idx >= 0 {
			cut = start + int64(idx) + 1
			break
		}
		read += chunk
	}
	probe.Close()
	// Windows：只写句柄不能 Truncate 自己的文件——关闭后重开再截断，再重开追加。
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("jsonl: close before truncate: %w", err)
	}
	if err := os.Truncate(path, cut); err != nil {
		return fmt.Errorf("jsonl: truncate: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("jsonl: reopen after truncate: %w", err)
	}
	w.f = f
	w.damagedTail = false
	return nil
}

// Close 关闭写入器。
func (w *Writer) Close() error {
	return w.f.Close()
}

// MaxLineBytes 是单行字节上限（防护性设计：单事件协议面不应达到；超限行
// 按损坏段隔离，不整体报错——T5-①）。数值与既有 Scanner 上限一致（16MB）。
const MaxLineBytes = 16 * 1024 * 1024

// headLimit 是超限行截断头的保留字节数（有界转储 FR-106；全行不缓冲）。
const headLimit = 64 * 1024

// OversizedSegment 是一条超限行的有界元信息（T5-①）。
type OversizedSegment struct {
	LineNo   int    // 1 起的行序（按文件物理行计）
	TotalLen int64  // 原始总字节数（不含行尾换行）
	Head     []byte // 前 headLimit 字节截断头（隔离转储用）
}

// Damage 是一次读取发现的损坏段汇总（两类来源可区分，FR-103）。
type Damage struct {
	HalfTail  bool               // 既有：尾部半行（崩溃残留，不计入完整行）
	Oversized []OversizedSegment // T5-①：超限行（隔离，不吞后续）
}

// Any 报告是否存在任何损坏（与旧 damaged bool 语义等价）。
func (d Damage) Any() bool { return d.HalfTail || len(d.Oversized) > 0 }

// Reader 读取 JSONL 文件（增量读：重复 ReadAll 可见新追加的行）。
type Reader struct {
	f *os.File
}

// OpenReader 打开读。
func OpenReader(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("jsonl: open reader: %w", err)
	}
	return &Reader{f: f}, nil
}

// ReadAll 返回当前全部完整行；检测到损坏尾部时返回错误且**不返回行**
// （损坏尾部不得静默降级为部分成功——需要部分行时用 ReadAllWithStatus）。
func (r *Reader) ReadAll() ([][]byte, error) {
	lines, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		return nil, err
	}
	if dmg.Any() {
		return nil, fmt.Errorf("jsonl: damaged segments detected")
	}
	return lines, nil
}

// ReadAllWithStatus 返回当前全部完整行与损坏段汇总：
// HalfTail=尾部半行（崩溃残留，既有语义）；Oversized=超限行（隔离为损坏段，
// 不吞后续内容——T5-① FR-101/102/103）。损坏段不计入完整行、不被静默丢弃
// ——由调用方决定修复或告警。
func (r *Reader) ReadAllWithStatus() (lines [][]byte, dmg Damage, err error) {
	if _, err := r.f.Seek(0, 0); err != nil {
		return nil, Damage{}, fmt.Errorf("jsonl: seek: %w", err)
	}
	br := bufio.NewReaderSize(r.f, 64*1024)
	lineNo := 0
	tailOversized := false // 最后一行无换行且超限（已计超限，不重复计半行）
	for {
		line, total, tooLong, hadNewline, rerr := readBoundedLine(br, MaxLineBytes)
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, Damage{}, fmt.Errorf("jsonl: read: %w", rerr)
		}
		lineNo++
		if tooLong {
			dmg.Oversized = append(dmg.Oversized,
				OversizedSegment{LineNo: lineNo, TotalLen: total, Head: line})
			tailOversized = !hadNewline
			continue
		}
		tailOversized = false
		lines = append(lines, line)
	}
	// 尾部判定（既有）：文件非空且最后字节非换行 → 最后一行是半行；
	// 若该尾行已按超限计（无换行超限），不重复计数、不误丢其前完整行。
	info, err := r.f.Stat()
	if err != nil {
		return nil, Damage{}, fmt.Errorf("jsonl: stat: %w", err)
	}
	if info.Size() > 0 && !tailOversized {
		buf := make([]byte, 1)
		if _, err := r.f.ReadAt(buf, info.Size()-1); err != nil {
			return nil, Damage{}, fmt.Errorf("jsonl: read last byte: %w", err)
		}
		if buf[0] != '\n' && len(lines) > 0 {
			dmg.HalfTail = true
			lines = lines[:len(lines)-1] // 半行不计入完整行
		}
	}
	return lines, dmg, nil
}

// readBoundedLine 读取一行（不含行尾换行）：字节总数 ≤ max 时正常返回
// （完整行逐字节返回——正常大行不得被截断，终审修正）；超过 max 时
// tooLong=true 且 line 为前 headLimit 字节截断头（超限段全行不缓冲），
// 继续消费到行尾/EOF——后续行不受影响（不吞内容）。ScanLines 语义保持：
// 剥离行尾 \r；空行返回空切片。EOF 且无任何字节 → io.EOF。
func readBoundedLine(br *bufio.Reader, max int) (line []byte, total int64, tooLong bool, hadNewline bool, err error) {
	// 双缓冲（终审修正）：buf 承载 ≤ max 的完整行（正常大行——大工具输出
	// 现实存在——必须逐字节返回）；head 只服务超限段的有界转储。一旦累计
	// 超过 max（over），buf 丢弃、仅 head 继续。
	var buf []byte
	var head []byte
	over := false
	absorb := func(chunk []byte) {
		total += int64(len(chunk))
		if !over {
			if int64(len(buf))+int64(len(chunk)) > int64(max) {
				over = true
				buf = nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if len(head) < headLimit {
			take := headLimit - len(head)
			if take > len(chunk) {
				take = len(chunk)
			}
			head = append(head, chunk[:take]...)
		}
	}
	for {
		chunk, ferr := br.ReadSlice('\n')
		if ferr == bufio.ErrBufferFull { // 缓冲满且未见换行：行继续
			absorb(chunk)
			continue
		}
		if ferr != nil {
			if ferr == io.EOF {
				if total == 0 && len(chunk) == 0 {
					return nil, 0, false, false, io.EOF
				}
				absorb(chunk)
				if !over && len(buf) > 0 && buf[len(buf)-1] == '\r' {
					buf = buf[:len(buf)-1] // ScanLines：EOF 终止 token 同样剥 \r
				}
				return headOr(buf, head, over), total, over, false, nil
			}
			return nil, 0, false, false, ferr
		}
		// chunk 以 '\n' 结尾：行完成（剥离 \n；既有 ScanLines 语义剥 \r）
		body := chunk[:len(chunk)-1]
		absorb(body)
		if !over && len(buf) > 0 && buf[len(buf)-1] == '\r' {
			buf = buf[:len(buf)-1] // ScanLines：剥离行尾 \r
		}
		return headOr(buf, head, over), total, over, true, nil
	}
}

// headOr 返回行字节：正常行=完整缓冲；超限行=截断头。
func headOr(buf, head []byte, over bool) []byte {
	if over {
		return head
	}
	return buf
}

// Close 关闭读取器。
func (r *Reader) Close() error {
	return r.f.Close()
}
