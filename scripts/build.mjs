#!/usr/bin/env node
// 组装可安装的插件包：后端二进制 + 内联页面的清单 → package/，再打成 dist/<id>-<version>.zip。
// 宿主支持目录 / ZIP / TGZ 安装；这里产出 ZIP 以便直接分发。
//
// 关键点：contributes.pages[].html.source 是**内联 HTML 文本**，不是文件路径。
// 宿主只对它做 UTF-8 与 256 KiB 校验，然后直接当成页面文档渲染；
// 写成 "frontend/index.html" 只会让插件页白屏。所以仓库根的 gonavi.plugin.json 是模板
// （html.source 为占位令牌），打包时把真实页面内容写进去，页面文件因此不必再进包。
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// 与模板清单里的占位令牌保持一致；单测会同时校验两边。
const INLINE_TOKEN = "__INLINE_FRONTEND_INDEX_HTML__";
// 页面源文件：内联之后清单里不再记录路径，这里的常量就是唯一来源。
const PAGE_ENTRY = "frontend/index.html";
// 宿主上限（internal/plugin/manifest_ui.go 的 MaxCustomPanelHTMLBytes）。
const MAX_HTML_BYTES = 256 * 1024;

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const templateRaw = readFileSync(path.join(root, "gonavi.plugin.json"), "utf8");
if (!templateRaw.includes(INLINE_TOKEN)) {
  throw new Error(`模板清单缺少内联占位令牌 ${INLINE_TOKEN}`);
}
const manifest = JSON.parse(templateRaw);

const stage = path.join(root, "package");
const distDir = path.join(root, "dist");
const out = path.join(distDir, `${manifest.id}-${manifest.version}.zip`);

// 清单声明了 backend.platforms，宿主要求包内每个条目文件都存在，所以逐平台构建。
const platforms = manifest.backend?.platforms ?? {};
if (Object.keys(platforms).length === 0) {
  throw new Error("清单没有 backend.platforms，无法确定后端入口");
}

rmSync(stage, { recursive: true, force: true });
mkdirSync(path.join(stage, "bin"), { recursive: true });
mkdirSync(distDir, { recursive: true });

for (const [platform, entry] of Object.entries(platforms)) {
  const [goos, goarch] = platform.split("/");
  execFileSync("go", ["build", "-trimpath", "-ldflags", "-s -w", "-o", path.join(stage, entry.executable), "./cmd/plugin"], {
    cwd: root,
    stdio: "inherit",
    env: { ...process.env, GOOS: goos, GOARCH: goarch, CGO_ENABLED: "0" },
  });
  console.log(`==> ${platform} -> ${entry.executable}`);
}

// 页面：把入口文档内容内联进清单。超出宿主上限或不是有效文本必须在这里失败，
// 而不是打出一个装得上却渲染不出来的包。
const entryPath = path.join(root, PAGE_ENTRY);
if (!existsSync(entryPath)) {
  throw new Error(`页面入口不存在：${PAGE_ENTRY}（前端产物未生成？）`);
}
const html = readFileSync(entryPath, "utf8");
const htmlBytes = Buffer.byteLength(html, "utf8");
if (htmlBytes > MAX_HTML_BYTES) {
  throw new Error(`页面 ${PAGE_ENTRY} 为 ${htmlBytes} 字节，超过宿主上限 ${MAX_HTML_BYTES}`);
}
if (html.trim() === "" || html.includes("\u0000")) {
  throw new Error(`页面 ${PAGE_ENTRY} 不是有效文本`);
}
const pages = manifest.contributes?.pages ?? [];
if (pages.length === 0) {
  throw new Error("清单没有 contributes.pages，无法确定页面入口");
}
const stagedPages = pages.map((page) => {
  if (page.html?.source !== INLINE_TOKEN) {
    throw new Error(`页面 ${page.id} 的 html.source 不是内联占位令牌`);
  }
  return { ...page, html: { ...page.html, source: html } };
});
manifest.contributes = { ...manifest.contributes, pages: stagedPages };
console.log(`==> 页面 ${PAGE_ENTRY} 内联进清单（${htmlBytes} 字节）`);

const stagedManifest = JSON.stringify(manifest, null, 2) + "\n";
if (Buffer.byteLength(stagedManifest, "utf8") > 1024 * 1024) {
  throw new Error("内联页面后清单超过宿主 1 MiB 上限");
}
writeFileSync(path.join(stage, "gonavi.plugin.json"), stagedManifest);

rmSync(out, { force: true });
// Windows 自带 bsdtar 能按扩展名自动选 zip，避免再依赖 zip 工具。
execFileSync("tar", ["-a", "-cf", out, "-C", stage, "bin", "gonavi.plugin.json"], { stdio: "inherit" });

console.log(`完成：${out}（${statSync(out).size} bytes）`);
