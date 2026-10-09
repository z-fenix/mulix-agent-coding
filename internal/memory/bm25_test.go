package memory

import (
	"reflect"
	"testing"
)

func TestTokenizeChineseBigrams(t *testing.T) {
	got := tokenize("记住本项目")
	want := []string{"记住", "住本", "本项", "项目"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize = %v, want %v", got, want)
	}
}

func TestTokenizeLatinWordsLowercased(t *testing.T) {
	got := tokenize("Use PNPM at root")
	want := []string{"use", "pnpm", "at", "root"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize = %v, want %v", got, want)
	}
}

func TestTokenizeMixed(t *testing.T) {
	got := tokenize(textBigram)
	want := []string{"记住", "住本", "本项", "项目", "目用", "pnpm"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize = %v, want %v", got, want)
	}
}

func TestTokenizeSingleCJKRuneUnigram(t *testing.T) {
	got := tokenize("记")
	want := []string{"记"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize = %v, want %v (single CJK rune degrades to unigram)", got, want)
	}
}

func TestBM25IndexScore(t *testing.T) {
	ix := newIndex()
	ix.add("a", "apple apple pie") // tf=2, dl=3
	ix.add("b", "apple tart cup")  // tf=1, dl=3（同长：tf 高者胜）
	ix.add("c", "banana bread")    // 不含查询词

	sa := ix.score("apple")
	if sa["a"] <= sa["b"] {
		t.Errorf("higher tf should outrank at equal doc length: %v", sa)
	}
	if _, ok := sa["c"]; ok {
		t.Errorf("non-matching doc must not score: %v", sa)
	}
	if len(ix.score("missing")) != 0 {
		t.Errorf("unknown term should score nothing")
	}
	if len(ix.score("")) != 0 {
		t.Errorf("empty query should score nothing")
	}

	// 同 tf，短文档胜（b=0.75 长度归一）。
	ix2 := newIndex()
	ix2.add("short", "apple tart")
	ix2.add("long", "apple tart cup spoon")
	s2 := ix2.score("apple")
	if s2["short"] <= s2["long"] {
		t.Errorf("shorter doc should outrank at equal tf: %v", s2)
	}

	// 移除后不再得分。
	ix.remove("a")
	if got := ix.score("apple"); len(got) != 1 {
		t.Errorf("after remove score = %v, want only b", got)
	}
	// 重复 add 同 id = 替换语义（不叠加词频）。
	ix.add("b", "apple")
	if got := ix.score("apple"); len(got) != 1 {
		t.Fatalf("after replace score = %v, want only b", got)
	}
	ix.add("b", "plum") // b 的 apple 词频被替换清除
	if got := ix.score("apple"); len(got) != 0 {
		t.Errorf("replaced doc keeps old terms: %v", got)
	}

	// 空索引。
	if got := newIndex().score("apple"); got != nil {
		t.Errorf("empty index score = %v, want nil", got)
	}
}
