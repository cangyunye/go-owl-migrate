package buildinfo

import (
	"strings"
	"testing"
)

// restoreVars 在测试结束后恢复包级变量——ldflags 注入目标，测试里手动
// 拨动时必须复原，避免影响同进程内其他测试。
func restoreVars(t *testing.T) {
	t.Helper()
	v, c, d := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = v, c, d })
}

// Short 是 UI 侧栏徽章的紧凑形态：永远只返回版本号本身。
func TestShortReturnsBareVersion(t *testing.T) {
	restoreVars(t)
	Commit, Date = "abc1234", "2026-10-06"
	if got := Short(); got != Version {
		t.Errorf("Short() = %q, want %q (badge must stay compact)", got, Version)
	}
}

// String 的契约：dev 构建（无 ldflags）保持短形态不添噪；注入了 commit/date
// 才展开完整三元组——`owl-migrate version` 与 /api/v1/version 共用此输出。
func TestStringAppendsMetadataOnlyWhenInjected(t *testing.T) {
	restoreVars(t)

	Commit, Date = "unknown", "unknown"
	if got := String(); got != Version {
		t.Errorf("dev build String() = %q, want bare %q", got, Version)
	}

	Commit, Date = "abc1234", "2026-10-06"
	got := String()
	if !strings.Contains(got, Version) || !strings.Contains(got, "abc1234") || !strings.Contains(got, "2026-10-06") {
		t.Errorf("String() should carry version+commit+date, got %q", got)
	}

	// 只注入其一也要展开（部分注入的 release 同样需要可追溯）。
	Commit, Date = "abc1234", "unknown"
	if got := String(); got == Version {
		t.Errorf("partially injected String() should expand, got %q", got)
	}
}
