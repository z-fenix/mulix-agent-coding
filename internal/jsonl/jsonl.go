// Package jsonl 提供 JSONL 追加日志：追加写（每行一 JSON，fsync 可选）、
// 增量读与损坏尾部检测（返回已完整行数与 damaged 标记）。sessionx 直接依赖。
// 红线：损坏尾部不得静默丢弃——读侧显式标记，写侧拒绝在损坏尾部上追加（需显式 Repair）。
// 本文件是包的权威文档；doc.go 仅保留短句。
package jsonl

import (
	"bufio"
	"bytes"
	"fmt"
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
	lines, damaged, err := r.ReadAllWithStatus()
	if err != nil {
		return nil, err
	}
	if damaged {
		return nil, fmt.Errorf("jsonl: damaged tail detected")
	}
	return lines, nil
}

// ReadAllWithStatus 返回当前全部完整行与损坏尾部标记：
// damaged=true 表示文件尾部存在未完成的半行（崩溃残留），已完整行数以返回值为准。
// 损坏尾部**不被静默丢弃**——由调用方决定修复或告警。
func (r *Reader) ReadAllWithStatus() (lines [][]byte, damaged bool, err error) {
	if _, err := r.f.Seek(0, 0); err != nil {
		return nil, false, fmt.Errorf("jsonl: seek: %w", err)
	}
	sc := bufio.NewScanner(r.f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		b := sc.Bytes()
		// Scanner 逐行扫描；最后一行无换行时 Scan 仍返回该行内容——
		// 需要用底层状态区分「完整行」与「损坏尾部半行」。
		lines = append(lines, append([]byte(nil), b...))
	}
	if err := sc.Err(); err != nil {
		return nil, false, fmt.Errorf("jsonl: scan: %w", err)
	}
	// 尾部判定：文件非空且最后一个字节非换行 → 最后一行是半行。
	info, err := r.f.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("jsonl: stat: %w", err)
	}
	if info.Size() > 0 {
		buf := make([]byte, 1)
		if _, err := r.f.ReadAt(buf, info.Size()-1); err != nil {
			return nil, false, fmt.Errorf("jsonl: read last byte: %w", err)
		}
		if buf[0] != '\n' && len(lines) > 0 {
			damaged = true
			lines = lines[:len(lines)-1] // 半行不计入完整行
		}
	}
	return lines, damaged, nil
}

// Close 关闭读取器。
func (r *Reader) Close() error {
	return r.f.Close()
}
