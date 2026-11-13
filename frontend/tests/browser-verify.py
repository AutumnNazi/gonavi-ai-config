# -*- coding: utf-8 -*-
"""浏览器验收：把 frontend/index.html 放进与宿主一致的不透明源 CSP 包装里，
注入一个假宿主桥跑真实 Chromium，逐条断言契约行为并留下三张同视口同主题的截图。

运行：python frontend/tests/browser-verify.py
说明：这里跑的不是宿主内的真实页面（GoNavi 标签页），而是同源文件 + 同一份 CSP + 假桥；
      真实宿主内的运行情况见交付说明里的「未在宿主内运行」标注。
"""
import json
import pathlib
import shutil
import sys

from playwright.sync_api import sync_playwright

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent
PAGE = (ROOT / "index.html").read_text(encoding="utf-8")
PREVIEW = HERE / ".preview"
SHOTS = HERE / "screenshots"

# 与宿主 sandboxedPanelDocument 的 CSP 一致：除内联脚本/样式与 data: 资源外全部禁用。
CSP = ("default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; "
       "img-src data: blob:; font-src data:; media-src data:; connect-src 'none'; "
       "form-action 'none'; navigate-to 'none'; base-uri 'none'; frame-src 'none'; object-src 'none'")

MOCK = """
(function () {
  var S = __SCENARIO__;
  window.gonaviHostPrefs = S.prefs;
  window.__calls = [];
  window.gonavi = {
    call: function (method, args) {
      var command = args && args.command;
      window.__calls.push({ method: method, command: command, args: args });
      if (method !== 'plugin.invoke') return Promise.reject(new Error('mock: ' + method));
      if (command === 'sources.scan') {
        if (S.scanError) return Promise.reject(new Error(S.scanError));
        return Promise.resolve(S.scan);
      }
      if (command === 'import.apply') {
        var entries = (args.arguments && args.arguments.items) || [];
        var drop = S.importDrop || [];
        var counters = { imported: 0, skipped: 0, failed: 0 };
        var items = [];
        var names = S.importNames || [];
        // 与插件后端一致：取不到配置的条目根本不进宿主请求，逐条以 index=-1 追加为 failed/source_missing。
        entries.forEach(function (entry, index) {
          if (drop.indexOf(index) >= 0) return;
          var spec = (S.importStatuses || [])[items.length] || { status: 'imported' };
          var status = spec.status || 'imported';
          if (counters[status] === undefined) status = 'failed';
          counters[status] += 1;
          items.push({ index: items.length, name: names[index] || '', status: status,
                       providerId: spec.providerId || '', reason: spec.reason || '' });
        });
        drop.forEach(function () {
          counters.failed += 1;
          items.push({ index: -1, name: '', status: 'failed', providerId: '', reason: 'source_missing' });
        });
        return Promise.resolve({
          imported: counters.imported, skipped: counters.skipped, failed: counters.failed,
          resolved: S.resolved === undefined ? entries.length : S.resolved, items: items
        });
      }
      return Promise.reject(new Error('mock: ' + command));
    },
    notify: function (message, type) { window.__calls.push({ method: 'ui.notify', message: message, type: type }); }
  };
})();
"""

SOURCE_CC = {"id": "cc-switch", "label": "CC Switch", "detected": True,
             "path": r"C:\Users\cky\.cc-switch\cc-switch.db", "count": 12, "error": ""}
SOURCE_MAGPIE = {"id": "magpie", "label": "Magpie", "detected": True,
                 "path": r"C:\Users\cky\.cache\magpie\providers-state.json", "count": 5, "error": "",
                 "keyUnavailable": True}


def entry(source, key, name, fmt, url, models=None, has_key=True, conflict=False, current=False, app="claude"):
    return {"sourceId": source, "sourceKey": key, "name": name, "baseUrl": url, "apiFormat": fmt,
            "model": (models or [""])[0], "models": models or [], "hasKey": has_key,
            "appType": app, "current": current, "conflict": conflict}


# 26 条数据：前 20 条无标记（首屏之外），随后 3 条冲突、3 条缺密钥，
# 让「主视图」与「冲突/缺密钥」两张截图在同一个视口里各看一半，而不是同一屏拍两次。
CLEAN = [
    ("claude:0c1f", "千刀", "anthropic", "https://api.zzzcoding.org", ["claude-opus-5"], "claude"),
    ("claude:5a72", "百倍", "anthropic", "https://api.baibei.ai", ["claude-opus-5", "claude-sonnet-5"], "claude"),
    ("claude:9d03", "Official", "anthropic", "https://api.anthropic.com", ["claude-opus-5"], "claude"),
    ("codex:11b8", "OpenRouter", "openai-responses", "https://openrouter.ai/api/v1", ["gpt-5-codex"], "codex"),
    ("codex:2c44", "DeepSeek", "openai", "https://api.deepseek.com/v1", ["deepseek-chat"], "codex"),
    ("gemini:7e91", "Gemini", "gemini", "https://generativelanguage.googleapis.com", ["gemini-2.5-pro"], "gemini"),
    ("claude:3f58", "Moonshot", "anthropic", "https://api.moonshot.cn/anthropic", ["kimi-k2"], "claude"),
    ("claude:8b21", "SiliconFlow", "openai", "https://api.siliconflow.cn/v1", ["deepseek-v3"], "claude"),
    ("claude:1d40", "百炼", "openai", "https://dashscope.aliyuncs.com/compatible-mode/v1", ["qwen3-max"], "claude"),
    ("claude:6f92", "智谱", "openai", "https://open.bigmodel.cn/api/paas/v4", ["glm-4.6"], "claude"),
    ("claude:2b87", "MiniMax", "anthropic", "https://api.minimaxi.com/anthropic", ["MiniMax-M2"], "claude"),
    ("codex:4e15", "Groq", "openai", "https://api.groq.com/openai/v1", ["qwen3-32b"], "codex"),
    ("codex:8a63", "Together", "openai", "https://api.together.xyz/v1", ["deepseek-v3"], "codex"),
    ("claude:5c09", "火山方舟", "openai", "https://ark.cn-beijing.volces.com/api/v3", ["doubao-seed"], "claude"),
    ("claude:0f71", "Cloudflare", "openai", "https://api.cloudflare.com/client/v4/accounts/x/ai/v1", ["gpt-4o"], "claude"),
    ("gemini:3a28", "Vertex", "gemini", "https://us-central1-aiplatform.googleapis.com", ["gemini-2.5-pro"], "gemini"),
    ("claude:7d44", "Azure", "openai", "https://my-resource.openai.azure.com/openai/v1", ["gpt-4o"], "claude"),
    ("claude:9b30", "Ollama 远程", "openai", "http://192.168.1.20:11434/v1", ["qwen3:32b"], "claude"),
    ("codex:1a96", "xAI", "openai", "https://api.x.ai/v1", ["grok-4"], "codex"),
    ("claude:3c52", "Mistral", "openai", "https://api.mistral.ai/v1", ["mistral-large-latest"], "claude"),
]


def main_scenario():
    items = [entry("cc-switch", key, name, fmt, url, models, app=app) for key, name, fmt, url, models, app in CLEAN]
    items += [
        entry("cc-switch", "claude:4d77", "千刀", "anthropic", "https://api.zzzcoding.org", ["claude-opus-5"], conflict=True, app="claude"),
        entry("cc-switch", "codex:6a19", "OpenRouter", "openai", "https://openrouter.ai/api/v1", ["gpt-5"], conflict=True, app="codex"),
        entry("cc-switch", "claude:0e35", "本地代理", "openai", "http://127.0.0.1:8317/v1", ["gpt-4o"], conflict=True, app="claude"),
        entry("magpie", "m-77ac", "AwsBedrock", "anthropic", "https://bedrock-runtime.us-east-1.amazonaws.com",
              ["claude-opus-4-1"], has_key=False, app="codex"),
        entry("magpie", "m-91de", "VertexClaude", "anthropic", "https://us-central1-aiplatform.googleapis.com",
              ["claude-opus-4-1"], has_key=False, current=True, app="claude"),
        entry("magpie", "m-c204", "LocalLlama", "openai", "http://localhost:11434/v1", [], has_key=False, app="codex"),
    ]
    statuses = [{"status": "imported", "providerId": "provider-%08x" % (0x8b190256 + index * 0x11f3)} for index in range(20)]
    statuses += [{"status": "skipped", "reason": "duplicate"}] * 3
    cc = dict(SOURCE_CC)
    cc["count"] = 23
    magpie = dict(SOURCE_MAGPIE)
    magpie["count"] = 3
    # 宿主返回的每条结果自带 name（后端原样转交），页面只认这个名字。
    names = [item["name"] for item in items]
    return {"prefs": {"locale": "zh-CN", "theme": "light"}, "scan": {"sources": [cc, magpie], "items": items},
            "importStatuses": statuses, "importNames": names, "importDrop": [23, 24, 25], "resolved": 23}


def scenario(name):
    if name == "main":
        return main_scenario()
    if name == "empty":
        return {"prefs": {"locale": "zh-CN", "theme": "light"},
                "scan": {"sources": [SOURCE_CC, SOURCE_MAGPIE], "items": []}}
    if name == "nodetect":
        return {"prefs": {"locale": "zh-CN", "theme": "light"},
                "scan": {"sources": [{"id": "cc-switch", "label": "CC Switch", "detected": False, "path": "", "count": 0, "error": ""},
                                     {"id": "magpie", "label": "Magpie", "detected": False, "path": "", "count": 0, "error": ""}], "items": []}}
    if name == "source_error":
        magpie = dict(SOURCE_MAGPIE)
        magpie.update({"detected": False, "error": "providers-state.json 不是合法 JSON"})
        return {"prefs": {"locale": "zh-CN", "theme": "light"},
                "scan": {"sources": [SOURCE_CC, magpie], "items": [entry("cc-switch", "claude:0c1f", "千刀", "anthropic", "https://api.zzzcoding.org")]}}
    if name == "scan_fail":
        return {"prefs": {"locale": "zh-CN", "theme": "light"}, "scanError": "插件后端进程没有响应"}
    if name == "skip_notes":
        cc = dict(SOURCE_CC)
        cc.update({"count": 2, "skipped": 3})
        magpie = dict(SOURCE_MAGPIE)
        magpie.update({"count": 1, "disabled": 24, "skipped": 7})
        return {"prefs": {"locale": "zh-CN", "theme": "light"},
                "scan": {"sources": [cc, magpie], "items": [
                    entry("cc-switch", "claude:0c1f", "千刀", "anthropic", "https://api.zzzcoding.org"),
                    entry("magpie", "m-77ac", "AwsBedrock", "anthropic",
                          "https://bedrock-runtime.us-east-1.amazonaws.com", has_key=False, app="codex"),
                ]}}
    if name == "paged":
        items = [entry("cc-switch", "claude:p%03d" % index, "P%03d" % index, "anthropic",
                       "https://api%d.example.com" % index, ["claude-opus-5"]) for index in range(100)]
        items += [entry("magpie", "m-%03d" % index, "M%03d" % index, "openai",
                        "https://m%d.example.com" % index, ["gpt-4o"], app="codex") for index in range(20)]
        cc = dict(SOURCE_CC)
        cc["count"] = 100
        magpie = dict(SOURCE_MAGPIE)
        magpie.update({"count": 20, "keyUnavailable": False})
        return {"prefs": {"locale": "zh-CN", "theme": "light"}, "scan": {"sources": [cc, magpie], "items": items}}
    if name == "english":
        data = main_scenario()
        data["prefs"] = {"locale": "en-US", "theme": "dark"}
        return data
    raise SystemExit("未知场景 " + name)


def build_preview(name):
    PREVIEW.mkdir(parents=True, exist_ok=True)
    mock = MOCK.replace("__SCENARIO__", json.dumps(scenario(name), ensure_ascii=False))
    html = PAGE.replace("<head>", '<head>\n<meta http-equiv="Content-Security-Policy" content="' + CSP + '">', 1)
    html = html.replace("<body>", "<body>\n<script>" + mock + "</script>", 1)
    path = PREVIEW / (name + ".html")
    path.write_text(html, encoding="utf-8")
    return path


RESULTS = []


def check(name, ok, detail=""):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + ("" if ok else "  <- " + str(detail)))


def text(page, selector):
    return page.eval_on_selector(selector, "node => node.textContent")


def run():
    paths = {name: build_preview(name) for name in
             ("main", "empty", "nodetect", "source_error", "scan_fail", "english", "skip_notes", "paged")}
    SHOTS.mkdir(parents=True, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch()
        context = browser.new_context(viewport={"width": 1280, "height": 900}, device_scale_factor=1,
                                      locale="zh-CN", color_scheme="light")
        page = context.new_page()
        console_errors, requests = [], []
        page.on("pageerror", lambda error: console_errors.append("pageerror: " + str(error)))

        def on_console(message):
            # 宿主 CSP 里的 navigate-to 在 Chromium 上尚未实现，会报一条与本页无关的告警。
            if message.type != "error" or "Unrecognized Content-Security-Policy directive" in message.text:
                return
            console_errors.append("console.error: " + message.text)

        page.on("console", on_console)
        page.on("request", lambda request: requests.append(request.url))

        # ---- 场景一：扫描到数据的主视图 ----
        page.goto(paths["main"].as_uri())
        page.wait_for_selector("#list table.list")
        cards = page.eval_on_selector_all("#sources .source", "nodes => nodes.map(n => n.textContent)")
        # 本次 scan 只回 2 个来源，契约 4.1 里的其余来源补成「宿主没有返回」的卡片。
        check("两个来源显示检测状态/路径/条数，未返回的 3 个来源出补齐卡片",
              len(cards) == 5 and "已检测到" in cards[0] and "23 条" in cards[0]
              and "cc-switch.db" in cards[0] and "3 条" in cards[1] and "providers-state.json" in cards[1]
              and all("未检测到" in card and "宿主没有返回这个来源的扫描结果" in card for card in cards[2:]), cards)
        check("条目列表渲染 26 行", page.eval_on_selector_all("#list tr.row", "n => n.length") == 26)
        below_fold = page.eval_on_selector_all("#list tr.row", "nodes => nodes[20].getBoundingClientRect().top > window.innerHeight")
        check("冲突与缺密钥条目在首屏之外（两张截图内容不同）", below_fold is True, below_fold)
        check("零选中态有文案且导入按钮禁用",
              "还没有勾选条目" in text(page, "#selection")
              and page.eval_on_selector("#import", "n => n.disabled") is True)
        check("表头列名完整", all(word in text(page, "#list") for word in ["名称", "来源", "接口格式", "地址", "模型", "标记"]))
        check("页面零外链：全部请求都是本地文件",
              all(url.startswith("file://") for url in requests), requests)
        check("加载与扫描期间没有未捕获异常", not console_errors, console_errors)
        page.screenshot(path=str(SHOTS / "01-main.png"))

        # ---- 场景四：按软件筛选（chips 只影响表格可见行） ----
        chips = page.eval_on_selector_all("#filters .chip", "nodes => nodes.map(n => n.textContent)")
        check("筛选 chips：全部 + 每个来源各一个，没检测到的来源置灰计 0",
              len(chips) == 6 and chips[0] == "全部"
              and page.eval_on_selector("#filters .chip[data-source='cc-switch']", "n => n.getAttribute('data-count')") == "23"
              and page.eval_on_selector("#filters .chip[data-source='magpie']", "n => n.getAttribute('data-count')") == "3"
              and page.eval_on_selector("#filters .chip[data-source='ccgui']", "n => n.getAttribute('data-count')") == "0"
              and page.eval_on_selector("#filters .chip[data-source='ccgui']", "n => n.getAttribute('data-empty')") == "1", chips)
        check("默认「全部」可见 26 / 共 26 条", "可见 26 / 共 26 条" in text(page, "#visible"))
        page.eval_on_selector("#filters .chip[data-source='magpie']", "node => node.click()")
        page.wait_for_timeout(120)
        check("筛选到单一来源只渲染该来源的行",
              page.eval_on_selector_all("#list tr.row", "n => n.length") == 3
              and "可见 3 / 共 26 条" in text(page, "#visible")
              and page.eval_on_selector("#filters .chip[data-source='magpie']", "n => n.getAttribute('data-on')") == "1"
              and page.eval_on_selector("#filters .chip[data-all]", "n => n.getAttribute('data-on')") == "0")
        page.screenshot(path=str(SHOTS / "04-filter-single-source.png"))
        page.eval_on_selector("#filters .chip[data-all]", "node => node.click()")
        page.wait_for_timeout(120)
        check("点「全部」清空筛选回到 26 行",
              page.eval_on_selector_all("#list tr.row", "n => n.length") == 26
              and "可见 26 / 共 26 条" in text(page, "#visible"))

        # ---- 场景二：冲突标记与 hasKey=false 提示 ----
        flags = page.eval_on_selector_all("#list tr.row .cell-flags", "nodes => nodes.map(n => n.textContent)")
        conflict_rows = page.eval_on_selector_all("#list tr.row", "nodes => nodes.filter(n => n.getAttribute('data-conflict') === '1').length")
        check("冲突条目带冲突标记并高亮", conflict_rows == 3 and flags[20].startswith("冲突") and flags[21].startswith("冲突")
              and flags[22].startswith("冲突"))
        check("hasKey=false 条目提示密钥需手工补填", "密钥需手工补填" in flags[23] and "密钥需手工补填" in flags[24]
              and "密钥需手工补填" in flags[25])
        check("来源级密钥缺口提示挂在 magpie 卡片上", "密钥需手工补填" in text(page, "#sources"))
        check("冲突标记带解释性 title", page.eval_on_selector("#list tr.row[data-conflict='1'] .flag", "n => n.getAttribute('title')").startswith("已有同名同地址"))
        page.evaluate("window.scrollTo(0, document.body.scrollHeight)")
        page.wait_for_timeout(150)
        in_view = page.eval_on_selector_all("#list tr.row", "nodes => nodes[20].getBoundingClientRect().top < window.innerHeight")
        check("滚动后冲突与缺密钥条目进入视口（截图②与①不同）", in_view is True, in_view)
        page.screenshot(path=str(SHOTS / "02-conflict-nokey.png"))

        # ---- 场景三：导入结果回显 ----
        page.eval_on_selector("#select-all", "node => node.click()")
        check("全选后计数与按钮可用", "已选 26 条" in text(page, "#selection")
              and page.eval_on_selector("#import", "n => n.disabled") is False)
        page.eval_on_selector("#overwrite", "node => { node.checked = true; node.dispatchEvent(new Event('change')); }")
        page.eval_on_selector("#import", "node => node.click()")
        page.wait_for_selector("#result .res-row")
        summary = text(page, "#result")
        statuses = page.eval_on_selector_all("#result .res-row", "nodes => nodes.map(n => n.getAttribute('data-status'))")
        check("导入结果给出汇总计数", "成功 20 条，跳过 3 条，失败 3 条" in summary and "其中 23 条取到了完整配置" in summary)
        check("逐条回显 imported/skipped/failed", statuses.count("imported") == 20 and statuses.count("skipped") == 3 and statuses.count("failed") == 3, statuses)
        names = page.eval_on_selector_all("#result .res-name", "nodes => nodes.map(n => n.textContent)")
        check("reason 用人话解释", "已存在同名同地址的供应商" in summary and "来源里已经找不到这条配置" in summary)
        check("取不到配置的条目如实标注名称不可用，不按序号错认",
              names[-3:] == ["（名称不可用）", "（名称不可用）", "（名称不可用）"]
              and "其中 3 条结果没有带回供应商名称" in summary
              and "返回了" not in summary, names[-3:])
        check("导入结果带供应商 ID", "provider-8b190256" in summary)
        check("导入后向宿主发了 toast 通知",
              any(call.get("method") == "ui.notify" and "导入完成" in call.get("message", "")
                  for call in page.evaluate("window.__calls")))
        check("导入请求按契约带上 overwrite 与 sourceKey",
              page.evaluate("""() => {
                const call = window.__calls.find(c => c.command === 'import.apply');
                return call.args.command === 'import.apply' && call.args.arguments.items.length === 26
                  && call.args.arguments.items[0].overwrite === true
                  && typeof call.args.arguments.items[0].sourceKey === 'string';
              }""") is True)
        page.eval_on_selector("#result", "node => node.scrollIntoView({block: 'start'})")
        page.screenshot(path=str(SHOTS / "03-import-result.png"))

        # ---- 反选仍然可用（导入完成后列表不被禁用） ----
        page.eval_on_selector("#invert", "node => node.click()")
        check("反选把已选清空", "还没有勾选条目" in text(page, "#selection"))

        # ---- 空态 / 未检测到 / 来源 error / 整体失败 ----
        page.goto(paths["empty"].as_uri())
        page.wait_for_selector("#list .empty")
        check("空态①检测到但零条目", "已检测到配置文件，但里面没有可导入的供应商" in text(page, "#list"))

        page.goto(paths["nodetect"].as_uri())
        page.wait_for_selector("#list .empty")
        # 宿主只回了这 2 个来源，页面另外补 3 张「宿主没有返回」的卡片，所以未检测到共 5 张卡。
        check("空态②报回来的来源都未检测到，空态点名说清楚", "未检测到 cc switch 与 magpie 的配置文件" in text(page, "#list")
              and text(page, "#sources").count("未检测到") == 5)

        page.goto(paths["source_error"].as_uri())
        page.wait_for_selector("#list tr.row")
        check("某来源 error 态只影响自己",
              "读取失败：providers-state.json 不是合法 JSON" in text(page, "#sources")
              and page.eval_on_selector_all("#list tr.row", "n => n.length") == 1
              and text(page, "#sources").count("已检测到") == 1)

        page.goto(paths["scan_fail"].as_uri())
        page.wait_for_selector("#banner:not([hidden])")
        check("整体扫描失败给出原因与重试入口",
              "扫描失败：插件后端进程没有响应" in text(page, "#banner")
              and page.eval_on_selector_all("#banner button", "n => n.length") == 1)

        page.goto(paths["english"].as_uri())
        page.wait_for_selector("#list table.list")
        check("英文宿主语言下全部文案切英文",
              "Import AI providers" in text(page, "#app-title") and "Detected" in text(page, "#sources")
              and "Path" in text(page, "#sources") and "Conflict" in text(page, "#list")
              and "Key needed" in text(page, "#list") and page.eval_on_selector("html", "n => n.getAttribute('data-theme')") == "dark")

        # ---- 场景五：停用与结构不支持两条说明同时出现 ----
        page.goto(paths["skip_notes"].as_uri())
        page.wait_for_selector("#list tr.row")
        notes = page.eval_on_selector_all("#sources .source-note", "nodes => nodes.map(n => n.textContent)")
        check("已停用与已跳过两条说明独立渲染、可同现",
              "已停用 24 条（在源工具里关闭，启用后即可导入）" in notes
              and "已跳过 7 条：源里没有可用的 http 端点、结构暂不支持，或与其它条目重复" in notes
              and "已跳过 3 条：源里没有可用的 http 端点、结构暂不支持，或与其它条目重复" in notes
              and not any("暂时不认识的条目" in note for note in notes), notes)
        check("旧的合并文案已从页面删除", "暂时不认识的条目" not in page.content())
        page.eval_on_selector("#sources", "node => node.scrollIntoView({block: 'start'})")
        page.wait_for_timeout(150)
        page.screenshot(path=str(SHOTS / "05-skip-notes.png"))

        # ---- 场景六：分页栏（跨页已勾选 + 跨页全选文案） ----
        page.goto(paths["paged"].as_uri())
        page.wait_for_selector("#pager:not([hidden])")
        check("默认每页 50 条、120 条分 3 页", page.eval_on_selector_all("#list tr.row", "n => n.length") == 50
              and "第 1 / 3 页" in text(page, "#page-info") and "共 120 条" in text(page, "#page-total")
              and page.eval_on_selector("#page-prev", "n => n.disabled") is True
              and page.eval_on_selector("#page-next", "n => n.disabled") is False)
        check("跨页全选文案写出筛选后条数并说明包含其它页",
              "全选（筛选后 120 条）" in text(page, "#select-all")
              and "包含其它页" in page.eval_on_selector("#select-all", "n => n.getAttribute('title')"))
        page.eval_on_selector("#list tr.row", "node => { const box = node.querySelector('input'); box.checked = true; box.dispatchEvent(new Event('change')); }")
        page.eval_on_selector("#page-next", "node => node.click()")
        page.wait_for_timeout(120)
        check("翻页后已勾选总数跨页合计",
              "已勾选 1 条（跨页合计）" in text(page, "#pager-selected")
              and "已选 1 条（跨页合计）" in text(page, "#selection")
              and "第 2 / 3 页" in text(page, "#page-info"))
        page.eval_on_selector("#list tr.row", "node => { const box = node.querySelector('input'); box.checked = true; box.dispatchEvent(new Event('change')); }")
        page.wait_for_timeout(120)
        check("第 2 页勾选后累计 2 条，切页不丢勾选",
              "已勾选 2 条（跨页合计）" in text(page, "#pager-selected")
              and page.eval_on_selector("#list tr.row input", "n => n.checked") is True)
        page.evaluate("window.scrollTo(0, 0)")
        page.wait_for_timeout(150)
        framed = page.eval_on_selector("#pager", "n => { const r = n.getBoundingClientRect(); return r.top >= 0 && r.bottom <= window.innerHeight + 1; }")
        check("分页栏粘在视口底部，截图里与工具条同框", framed is True, framed)
        page.screenshot(path=str(SHOTS / "06-pagination.png"))

        page.select_option("#page-size", "20")
        page.wait_for_timeout(120)
        check("改每页条数回到第 1 页", "第 1 / 6 页" in text(page, "#page-info")
              and page.eval_on_selector_all("#list tr.row", "n => n.length") == 20)
        page.eval_on_selector("#filters .chip[data-source='magpie']", "node => node.click()")
        page.wait_for_timeout(120)
        check("筛选变化回到第 1 页，夹到末页不留空白",
              "第 1 / 1 页" in text(page, "#page-info")
              and page.eval_on_selector_all("#list tr.row", "n => n.length") == 20
              and page.eval_on_selector("#page-prev", "n => n.disabled") is True)
        page.eval_on_selector("#clear-selection", "node => node.click()")
        page.wait_for_timeout(120)
        check("取消全选清空跨页勾选", "已勾选 0 条（跨页合计）" in text(page, "#pager-selected")
              and "还没有勾选条目" in text(page, "#selection"))

        check("全程没有未捕获异常与 CSP 违规", not console_errors, console_errors)
        context.close()
        browser.close()

    # 预览是脚本每次重建的中间产物，跑完清掉，仓库里只留截图证据。
    shutil.rmtree(PREVIEW, ignore_errors=True)
    failed = [name for name, ok, _ in RESULTS if not ok]
    print("\n%d/%d 通过" % (len(RESULTS) - len(failed), len(RESULTS)))
    for name in failed:
        print("未通过：" + name)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(run())
