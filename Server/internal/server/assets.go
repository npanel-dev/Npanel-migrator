package server

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// embeddedAssets 把 internal/server/assets 目录嵌入二进制。
// assets 由 Makefile 的 copy-frontend 目标从 ../Vue/dist 拷贝而来。
// 注意：//go:embed 指令必须紧贴下面的变量声明，中间不能有空行。
//
//go:embed all:assets
var embeddedAssets embed.FS

// newSPAHandler 用 embed.FS 托管前端构建产物，并提供 SPA history fallback。
//
// 行为：
//  1. 请求路径命中真实文件（如 /assets/index-xxx.js、/favicon.ico）-> 直接返回该文件；
//  2. 请求路径不以 /api 开头且不是真实文件（如 /migration、/settings 等
//     前端路由）-> 返回 index.html，交给前端 vue-router 处理；
//  3. 路径以 /api 开头 -> 不在此处理（由业务路由或 404 兜底）。
//
// assetsFS 是去掉 "assets/" 前缀后的 fs.FS，使 Open 调用使用相对路径。
func newSPAHandler(efs embed.FS) http.Handler {
	subFS, err := fs.Sub(efs, "assets")
	if err != nil {
		// assets 目录缺失时 panic：开发期会被立即发现。
		// Makefile 在 go build 前会执行 copy-frontend 把 Vue/dist 拷入 assets/。
		panic("embedded 'assets' dir not found; run 'make build' to copy frontend dist")
	}
	fileServer := http.FileServer(http.FS(subFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /api 交给业务路由，这里不响应。
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
			http.NotFound(w, r)
			return
		}

		// 清理路径，防止目录穿越。
		clean := path.Clean(r.URL.Path)
		if clean == "/" {
			clean = "index.html"
		}

		// 命中真实文件 -> 直接服务。
		if f, err := subFS.Open(strings.TrimPrefix(clean, "/")); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// 未命中真实文件 -> 返回 index.html（SPA fallback）。
		// 设法读取 index.html。
		indexBytes, err := fs.ReadFile(subFS, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexBytes)
	})
}
