package engine

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRepositoryLock2301(t *testing.T) {
	lock, err := LoadEngineLock(filepath.Join("..", "..", "engine.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if lock.CLIVersion != "2.1.301" {
		t.Fatalf("want 2.1.301, got %q", lock.CLIVersion)
	}
	if _, err := time.Parse("2006-01-02", lock.PinnedAt); err != nil {
		t.Fatal(err)
	}
	if lock.PinnedAt == "2026-10-10" {
		t.Fatal("repin must record its actual new date")
	}
	if lock.ProtocolProbe.InitVersionField != "claude_code_version" {
		t.Fatal("probe contract changed")
	}
	if lock.Baseline.CacheHitRate != 0.953 {
		t.Fatal("historical sample changed")
	}
	for _, part := range []string{"2026-10-10 真实冒烟实测", "历史参考，未作2.1.301缓存重测"} {
		if !strings.Contains(lock.Baseline.Note, part) {
			t.Fatalf("missing provenance %q", part)
		}
	}
}
