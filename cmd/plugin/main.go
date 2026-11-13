// gonavi-ai-config 插件后端入口：stdin/stdout 上的 JSON-RPC 协议服务。
// stdout 只走协议帧，诊断写 stderr。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	goplugin "github.com/Syngnat/GoNavi/sdk/go"

	"github.com/AutumnNazi/gonavi-ai-config/internal/app"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gonavi-ai-config:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// 本插件不落任何自有数据：来源都在用户目录（cc-switch 库、magpie 状态文件），
	// 导入写入一律走宿主能力。因此不解析 GONAVI_PLUGIN_DATA，
	// 避免再造一个用不上的目录。
	service := app.New(nil)
	server := goplugin.NewServer(goplugin.Options{Handlers: service.Handlers()})
	return server.ServeStdio(ctx)
}
