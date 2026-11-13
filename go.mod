module github.com/AutumnNazi/gonavi-ai-config

go 1.24.0

require (
	github.com/Syngnat/GoNavi/sdk/go v0.0.0
	modernc.org/sqlite v1.44.3
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/exp v0.0.0-20251023183803-a4bb9ffd2546 // indirect
	golang.org/x/sys v0.37.0 // indirect
	modernc.org/libc v1.67.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

// 按插件作者指南 sdk.md 的约定消费 SDK：开发期用本地检出，require 写 v0.0.0、由 replace 指到
// 本机 GoNavi 仓库；发布的是打包归档而不是源码树，因此这里的绝对路径不影响分发。
replace github.com/Syngnat/GoNavi/sdk/go => E:/code/GoNavi/sdk/go
