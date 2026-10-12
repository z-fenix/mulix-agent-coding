package textutil

import (
	"reflect"
	"testing"
)

// TestTokenize 钉分词语义(CJK bigram/拉丁词/混合)——从 memory 测试迁移。
func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"记住本项目用 pnpm", []string{"记住", "住本", "本项", "项目", "目用", "pnpm"}},
		{"hello world", []string{"hello", "world"}},
		{"单字", []string{"单字"}},
		{"", nil},
	}
	for _, tc := range cases {
		got := Tokenize(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("Tokenize(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestBM25IndexScore 钉索引语义:命中正分;不命中不在结果。
func TestBM25IndexScore(t *testing.T) {
	ix := NewBM25Index()
	ix.Add("d1", "缓存穿透问题")
	ix.Add("d2", "权限治理")
	scores := ix.Score("缓存")
	if len(scores) != 1 {
		t.Fatalf("scores = %v, want 1 hit", scores)
	}
	if _, ok := scores["d1"]; !ok {
		t.Fatal("d1 must score")
	}
}
