// Package textutil 的 BM25 子集(013 P5a T1:从 memory/bm25.go 抽取——
// memory/sessionx 共用;算法与常量逐字节同源)。
package textutil

import (
	"math"
	"strings"
	"unicode"
)

// BM25 标准参数(design §4:k1=1.5, b=0.75)。
const (
	BM25K1 = 1.5
	BM25B  = 0.75
)

// BM25Index 是进程内 BM25 索引:全量构建,Add/Remove 增量更新。
// 结构取原生 map(docID → 词 → 词频),检索 O(文档数×查询词数)。
type BM25Index struct {
	docs     map[string]map[string]int
	docLen   map[string]int
	totalLen int
}

func NewBM25Index() *BM25Index {
	return &BM25Index{docs: map[string]map[string]int{}, docLen: map[string]int{}}
}

// Add 索引一篇文档;同 id 重复 add 为替换语义。
func (ix *BM25Index) Add(id, text string) {
	ix.Remove(id)
	tf := make(map[string]int)
	n := 0
	for _, tok := range Tokenize(text) {
		tf[tok]++
		n++
	}
	ix.docs[id] = tf
	ix.docLen[id] = n
	ix.totalLen += n
}

// Remove 摘除一篇文档;未索引的 id 为 no-op。
func (ix *BM25Index) Remove(id string) {
	n, ok := ix.docLen[id]
	if !ok {
		return
	}
	delete(ix.docLen, id)
	delete(ix.docs, id)
	ix.totalLen -= n
}

// Score 对查询打分,返回「含至少一个查询词」的文档得分。
func (ix *BM25Index) Score(query string) map[string]float64 {
	n := len(ix.docs)
	if n == 0 {
		return nil
	}
	avgdl := float64(ix.totalLen) / float64(n)
	scores := make(map[string]float64)
	for term := range termSet(Tokenize(query)) {
		df := 0
		for _, tf := range ix.docs {
			if tf[term] > 0 {
				df++
			}
		}
		if df == 0 {
			continue
		}
		idf := math.Log((float64(n-df)+0.5)/(float64(df)+0.5) + 1)
		for id, tf := range ix.docs {
			c := tf[term]
			if c == 0 {
				continue
			}
			dl := float64(ix.docLen[id])
			scores[id] += idf * (float64(c) * (BM25K1 + 1)) /
				(float64(c) + BM25K1*(1-BM25B+BM25B*dl/avgdl))
		}
	}
	return scores
}

func termSet(toks []string) map[string]struct{} {
	set := make(map[string]struct{}, len(toks))
	for _, tok := range toks {
		set[tok] = struct{}{}
	}
	return set
}

// Tokenize 分词(CJK bigram/拉丁词/混合)——导出面(memory/sessionx 共用)。
func Tokenize(text string) []string {
	var toks []string
	var cjk []rune
	var word strings.Builder

	flushCJK := func() {
		switch len(cjk) {
		case 0:
		case 1:
			toks = append(toks, string(cjk[0]))
		default:
			for i := 0; i+1 < len(cjk); i++ {
				toks = append(toks, string(cjk[i:i+2]))
			}
		}
		cjk = cjk[:0]
	}
	flushWord := func() {
		if word.Len() > 0 {
			toks = append(toks, strings.ToLower(word.String()))
			word.Reset()
		}
	}
	for _, r := range text {
		switch {
		case isCJK(r):
			flushWord()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			word.WriteRune(r)
		default:
			flushCJK()
			flushWord()
		}
	}
	flushCJK()
	flushWord()
	return toks
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// TermSet 查询词去重(导出面:memory wrapper 消费)。
func TermSet(toks []string) map[string]struct{} {
	set := make(map[string]struct{}, len(toks))
	for _, tok := range toks {
		set[tok] = struct{}{}
	}
	return set
}

// IsCJK 判断 CJK rune(导出面:memory wrapper 消费)。
func IsCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}
