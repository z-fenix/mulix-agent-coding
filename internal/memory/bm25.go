// Package memory 的 BM25 子集：核心已抽取至 textutil（013 P5a T1——
// memory/sessionx 共用）；本文件为 wrapper struct（既有调用点零语义变更——
// unexported 方法名保持，内部委托 textutil）。
package memory

import "mulix/internal/textutil"

// index 是 textutil.BM25Index 的包装 struct（非 alias——unexported 方法
// 名保持，既有调用点零语义变更）。
type index struct {
	inner *textutil.BM25Index
}

func newIndex() *index { return &index{inner: textutil.NewBM25Index()} }

// add 索引一篇文档（委托 textutil）。
func (ix *index) add(id, text string) { ix.inner.Add(id, text) }

// remove 摘除一篇文档（委托 textutil）。
func (ix *index) remove(id string) { ix.inner.Remove(id) }

// score 对查询打分（委托 textutil）。
func (ix *index) score(query string) map[string]float64 { return ix.inner.Score(query) }

// tokenize 分词（委托 textutil）。
func tokenize(text string) []string { return textutil.Tokenize(text) }

// termSet 查询词去重（委托 textutil）。
func termSet(toks []string) map[string]struct{} { return textutil.TermSet(toks) }

// isCJK 判断 CJK rune（委托 textutil）。
func isCJK(r rune) bool { return textutil.IsCJK(r) }
