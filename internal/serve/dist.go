// dist.go——前端构建产物嵌入(go:embed all:dist,2026-10-18-web-ui-mvp Task 3)。
//
// dist/index.html 占位页常驻入仓,保证任何分支 go:embed 可编译;真实构建
// (Task 9)产物经 .gitignore 三规则白名单原地替换。all: 前缀使下划线/点
// 开头文件亦被嵌入(vite 产物 assets 常见)。
package serve

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embeddedDist embed.FS

// distFS 重根后的 dist 子文件系统:URL / 直接映射 dist/index.html。
// 子目录存在性由编译期 embed 指令保证,错误分支不可达(panic 兜底)。
var distFS = mustSub(embeddedDist, "dist")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("serve: dist 子文件系统不可达: " + err.Error())
	}
	return sub
}
