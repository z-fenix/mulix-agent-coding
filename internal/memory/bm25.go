package memory

import (
	"math"
	"strings"
	"unicode"
)

// BM25 标准参数（design §4：k1=1.5, b=0.75）。
const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

// index 是进程内 BM25 索引：Open 全量重放构建，Add/Delete 增量更新。
// 结构取原生 map（docID → 词 → 词频），检索 O(文档数×查询词数) 的纯 map
// 查找——万级条目毫秒级，不引入倒排表复杂度（零依赖约束）。
type index struct {
	docs     map[string]map[string]int // docID → 词 → 词频
	docLen   map[string]int            // docID → 词条数（长度归一用）
	totalLen int                       // 全库词条总数（avgdl 分母）
}

func newIndex() *index {
	return &index{docs: map[string]map[string]int{}, docLen: map[string]int{}}
}

// add 索引一篇文档；同 id 重复 add 为替换语义（先清旧词频再入新），
// 重放遇到异常重复 add 行时保持词频不叠加。
func (ix *index) add(id, text string) {
	ix.remove(id)
	tf := make(map[string]int)
	n := 0
	for _, tok := range tokenize(text) {
		tf[tok]++
		n++
	}
	ix.docs[id] = tf
	ix.docLen[id] = n
	ix.totalLen += n
}

// remove 摘除一篇文档（Delete/tombstone 收敛路径）；未索引的 id 为 no-op。
func (ix *index) remove(id string) {
	n, ok := ix.docLen[id]
	if !ok {
		return
	}
	delete(ix.docLen, id)
	delete(ix.docs, id)
	ix.totalLen -= n
}

// score 对查询打分，返回「含至少一个查询词」的文档得分（正分；不匹配的
// 文档不在结果里）。BM25 取 Wikipedia 标准式：
//
//	idf = ln((N - n + 0.5) / (n + 0.5) + 1)   —— +1 变体，IDF 恒正
//	score = Σ idf · tf·(k1+1) / (tf + k1·(1 − b + b·dl/avgdl))
func (ix *index) score(query string) map[string]float64 {
	n := len(ix.docs)
	if n == 0 {
		return nil
	}
	avgdl := float64(ix.totalLen) / float64(n)
	scores := make(map[string]float64)
	for term := range termSet(tokenize(query)) {
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
			scores[id] += idf * (float64(c) * (bm25K1 + 1)) /
				(float64(c) + bm25K1*(1-bm25B+bm25B*dl/avgdl))
		}
	}
	return scores
}

// termSet 查询词去重：重复查询词不重复计分（幂等求和语义）。
func termSet(toks []string) map[string]struct{} {
	set := make(map[string]struct{}, len(toks))
	for _, tok := range toks {
		set[tok] = struct{}{}
	}
	return set
}

// tokenize 分词（design §4：中文按 rune bigram，英文按词）：
//   - CJK 连续段（Han/平假名/片假名/谚文）滑窗 bigram；段长 1 退化为 unigram，
//     保证单字查询仍可命中；
//   - 拉丁字母/数字连续段为一个词，转小写（大小写不敏感命中）；
//   - 其余 rune（空格/标点）作为分隔。
//
// 中英混合文本两边都出词（「记住本项目用 pnpm」→ 记住/住本/本项/项目/目用/pnpm）。
func tokenize(text string) []string {
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
