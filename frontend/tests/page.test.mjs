// 页面逻辑验收：把 index.html 的脚本放进 vm 跑，断言契约里的每一条可见行为。
// 运行：node --test frontend/tests/  （无任何外部依赖，也不需要浏览器）
import test from 'node:test';
import assert from 'node:assert/strict';
import { loadPage, makeBridge, scan, item, settle } from './harness.mjs';

function noScriptErrors(page) {
  assert.deepEqual(page.errors, [], '页面脚本不得在加载期抛错');
}

function rowBoxes(page) {
  return page.rows().map((row) => row.findAll('input')[0]);
}

function flagsText(page) {
  return page.rows().map((row) => row.findAll('.flag').map((node) => node.textContent).join('|'));
}

async function loaded(handlers, options = {}) {
  const bridge = makeBridge(handlers);
  const page = loadPage({ bridge, prefs: options.prefs });
  noScriptErrors(page);
  await settle();
  return { page, bridge };
}

test('打开页面即显示两个来源的检测状态与条目列表', async () => {
  const { page } = await loaded({ 'sources.scan': () => scan() });
  const cards = page.byId('sources').children;
  // 本次 scan 只回了 2 个来源，其余已知来源补成「宿主没有返回」的卡片，一个都不能少。
  assert.equal(cards.length, 5);
  assert.match(cards[2].textContent, /宿主没有返回这个来源的扫描结果/);
  assert.match(cards[0].textContent, /CC Switch/);
  assert.match(cards[0].textContent, /已检测到/);
  assert.match(cards[0].textContent, /cc-switch\.db/);
  assert.match(cards[0].textContent, /2 条/);
  assert.match(cards[1].textContent, /Magpie/);
  assert.match(cards[1].textContent, /providers-state\.json/);
  assert.match(cards[1].textContent, /1 条/);
  assert.equal(page.rows().length, 3);
  assert.match(page.byId('list').textContent, /名称/);
  assert.match(page.byId('list').textContent, /接口格式/);
  assert.match(page.byId('list').textContent, /地址/);
});

test('冲突条目标记、hasKey=false 提示与来源级密钥缺口提示', async () => {
  const { page } = await loaded({
    'sources.scan': () => scan({ magpie: { keyUnavailable: true } }),
  });
  const flags = flagsText(page);
  assert.equal(page.rows()[0].findAll('.cell-flags')[0].textContent, '—');
  assert.match(flags[1], /冲突/);
  assert.match(flags[2], /密钥需手工补填/);
  assert.equal(page.rows()[1].getAttribute('data-conflict'), '1');
  assert.equal(page.rows()[0].getAttribute('data-conflict'), '0');
  const conflictFlag = page.rows()[1].findAll('.flag')[0];
  assert.match(conflictFlag.getAttribute('title'), /同名同地址/);
  assert.match(page.byId('sources').textContent, /密钥需手工补填/);
});

test('全选 / 反选 / 单条勾选与零选中态', async () => {
  const { page } = await loaded({ 'sources.scan': () => scan() });
  assert.match(page.text('selection'), /还没有勾选条目/);
  assert.equal(page.byId('import').disabled, true);

  page.byId('select-all').click();
  assert.match(page.text('selection'), /已选 3 条/);
  assert.deepEqual(rowBoxes(page).map((box) => box.checked), [true, true, true]);
  assert.equal(page.byId('import').disabled, false);

  page.byId('invert').click();
  assert.deepEqual(rowBoxes(page).map((box) => box.checked), [false, false, false]);
  assert.equal(page.byId('import').disabled, true);

  const box = rowBoxes(page)[2];
  box.checked = true;
  box.dispatch('change');
  assert.match(page.text('selection'), /已选 1 条/);
  assert.deepEqual(rowBoxes(page).map((item) => Boolean(item.checked)), [false, false, true]);
});

test('超过契约上限 100 条时拦住导入并给出文案', async () => {
  const many = { sources: scan().sources, items: [] };
  for (let index = 0; index < 101; index += 1) many.items.push(item({ sourceKey: 'k' + index, name: 'P' + index }));
  const { page } = await loaded({ 'sources.scan': () => many });
  // 默认每页 50 条，101 条分 3 页；全选按「筛选后的全部条目（跨页）」算，
  // 上限拦截不能被分页悄悄放过。
  assert.equal(page.rows().length, 50);
  assert.match(page.text('page-info'), /第 1 \/ 3 页/);
  page.byId('select-all').click();
  assert.match(page.text('selection'), /已选 101 条/);
  assert.match(page.text('selection'), /一次最多导入 100 条/);
  assert.equal(page.byId('import').disabled, true);
});

test('导入后逐条回显 status 与 reason 的人话解释，并给出汇总计数', async () => {
  const { page, bridge } = await loaded({
    'sources.scan': () => scan(),
    'import.apply': (args) => ({
      imported: 1,
      skipped: 1,
      failed: 1,
      resolved: 2,
      items: [
        { index: 0, name: '千刀', status: 'imported', providerId: 'provider-8b190256', reason: '' },
        { index: 1, name: '百倍', status: 'skipped', providerId: '', reason: 'duplicate' },
        { index: 2, name: 'Magpie One', status: 'failed', providerId: '', reason: 'invalid_name' },
      ],
    }),
  });
  page.byId('select-all').click();
  page.byId('overwrite').checked = true;
  page.byId('overwrite').dispatch('change');
  page.byId('import').click();
  await settle();

  const payload = bridge.calls.find((call) => call.command === 'import.apply').args.arguments;
  assert.equal(payload.items.length, 3);
  // 跨 realm 的对象比较按 JSON 形态断言，避免原型不同造成的假失败。
  assert.deepEqual(JSON.parse(JSON.stringify(payload.items[1])), { sourceId: 'cc-switch', sourceKey: 'k2', overwrite: true });

  const result = page.byId('result');
  assert.equal(result.hidden, false);
  assert.match(result.textContent, /导入结果/);
  assert.match(result.textContent, /成功 1 条，跳过 1 条，失败 1 条/);
  assert.match(result.textContent, /其中 2 条取到了完整配置/);
  const rows = result.findAll('.res-row');
  assert.deepEqual(rows.map((row) => row.getAttribute('data-status')), ['imported', 'skipped', 'failed']);
  assert.match(rows[0].textContent, /已导入/);
  assert.match(rows[0].textContent, /provider-8b190256/);
  assert.match(rows[1].textContent, /已存在同名同地址的供应商/);
  assert.match(rows[2].textContent, /供应商名称去掉首尾空格后为空或超过 128 个字符/);
  const notify = bridge.calls.find((call) => call.method === 'ui.notify');
  assert.match(notify.message, /导入完成/);
});

test('宿主因请求头超限拒收时给出可归因文案（too_many_headers）', async () => {
  const { page } = await loaded({
    'sources.scan': () => scan(),
    'import.apply': () => ({
      imported: 0,
      skipped: 0,
      failed: 1,
      resolved: 1,
      items: [{ index: 0, name: '头太多', status: 'failed', providerId: '', reason: 'too_many_headers' }],
    }),
  });
  page.byId('select-all').click();
  page.byId('import').click();
  await settle();
  const row = page.byId('result').findAll('.res-row')[0];
  // 必须说清「请求头超上限所以没导入」，不能落到「未识别的原因：too_many_headers」。
  assert.match(row.textContent, /自定义请求头超过宿主上限/);
  assert.doesNotMatch(row.textContent, /未识别的原因/);
});

test('未知 reason 回退到通用文案，返回条数少于提交条数时如实说明', async () => {
  const { page } = await loaded({
    'sources.scan': () => scan(),
    'import.apply': () => ({
      imported: 0,
      skipped: 0,
      failed: 1,
      resolved: 1,
      items: [{ index: 0, name: '千刀', status: 'failed', providerId: '', reason: 'weird_code' }],
    }),
  });
  page.byId('select-all').click();
  page.byId('import').click();
  await settle();
  const rows = page.byId('result').findAll('.res-row');
  assert.equal(rows.length, 1);
  assert.match(rows[0].textContent, /未识别的原因：weird_code/);
  assert.equal(rows[0].getAttribute('data-status'), 'failed');
  assert.match(page.byId('result').textContent, /本次提交 3 条，宿主只返回了 1 条结果/);
});

test('后端跳过取不到配置的条目（index=-1）时不按序号错认名称', async () => {
  const picked = ['A', 'B', 'C', 'D'].map((name, index) => item({ sourceKey: 'k' + index, name: name }));
  const { page } = await loaded({
    'sources.scan': () => ({ sources: scan().sources, items: picked }),
    'import.apply': () => ({
      imported: 2,
      skipped: 1,
      failed: 1,
      // 后端只把能取到配置的 3 条发给宿主，序号 0/1/2 对应的是 A/B/D；取不到的 C 用 index=-1 且不带名称。
      items: [
        { index: 0, name: 'A', status: 'imported', providerId: 'provider-aaa', reason: '' },
        { index: 1, name: 'B', status: 'skipped', providerId: '', reason: 'duplicate' },
        { index: 2, name: 'D', status: 'imported', providerId: 'provider-ddd', reason: '' },
        { index: -1, name: '', status: 'failed', providerId: '', reason: 'source_missing' },
      ],
      resolved: 3,
    }),
  });
  page.byId('select-all').click();
  page.byId('import').click();
  await settle();
  const rows = page.byId('result').findAll('.res-row');
  const names = rows.map((row) => row.findAll('.res-name')[0].textContent);
  assert.deepEqual(names, ['A', 'B', 'D', '（名称不可用）']);
  assert.match(rows[3].textContent, /来源里已经找不到这条配置/);
  const summary = page.byId('result').textContent;
  assert.match(summary, /其中 1 条结果没有带回供应商名称/);
  assert.match(summary, /成功 2 条，跳过 1 条，失败 1 条/);
  assert.equal(summary.includes('返回了'), false);
});

test('空态：检测到但没有条目、两个来源都未检测到、来源结果缺失', async () => {
  const empty = { sources: scan().sources, items: [] };
  const first = await loaded({ 'sources.scan': () => empty });
  assert.match(first.page.byId('list').textContent, /已检测到配置文件，但里面没有可导入的供应商/);
  assert.equal(first.page.byId('import').disabled, true);

  const missingBoth = await loaded({
    'sources.scan': () => ({
      sources: [
        { id: 'cc-switch', label: 'CC Switch', detected: false, path: '', count: 0, error: '' },
        { id: 'magpie', label: 'Magpie', detected: false, path: '', count: 0, error: '' },
      ],
      items: [],
    }),
  });
  assert.match(missingBoth.page.byId('list').textContent, /未检测到 cc switch 与 magpie 的配置文件/);
  assert.match(missingBoth.page.byId('sources').textContent, /未检测到/);

  const omitted = await loaded({ 'sources.scan': () => ({ sources: [], items: [] }) });
  assert.equal(omitted.page.byId('sources').children.length, 5);
  assert.match(omitted.page.byId('sources').textContent, /宿主没有返回这个来源的扫描结果/);
});

test('某来源 error 态只影响自己，扫描整体失败给出重试入口', async () => {
  const { page } = await loaded({
    'sources.scan': () => scan({ magpie: { detected: false, error: '不是可读的 providers-state.json' } }),
  });
  const cards = page.byId('sources').children;
  assert.match(cards[1].textContent, /读取失败：不是可读的 providers-state\.json/);
  assert.match(cards[0].textContent, /已检测到/);
  assert.equal(page.rows().length, 3);

  let attempts = 0;
  const failing = loadPage({
    bridge: makeBridge({
      'sources.scan': () => {
        attempts += 1;
        if (attempts === 1) throw new Error('扫描进程超时');
        return scan();
      },
    }),
  });
  await settle();
  assert.match(failing.byId('banner').textContent, /扫描失败：扫描进程超时/);
  assert.equal(failing.byId('banner').hidden, false);
  const retry = failing.byId('banner').findAll('button')[0];
  retry.click();
  await settle();
  assert.equal(failing.byId('banner').hidden, true);
  assert.equal(failing.rows().length, 3);
  assert.deepEqual(failing.errors, []);
});

test('宿主失败信封与异常返回体都进入失败态，不留空白屏', async () => {
  const denied = await loaded({
    'sources.scan': () => ({ ok: false, error: { code: 'permission_denied' } }),
  });
  assert.match(denied.page.byId('banner').textContent, /宿主没有授予写入 AI 供应商的权限/);
  // 宿主整段没回 sources，点名不到具体来源，退回通用文案。
  assert.match(denied.page.byId('list').textContent, /未检测到任何支持的配置文件/);

  const bogus = await loaded({ 'sources.scan': () => 'nonsense' });
  assert.match(bogus.page.byId('banner').textContent, /扫描失败/);
  assert.equal(bogus.page.byId('result').hidden, true);
});

test('宿主没有导入能力时把 unknown_method 归因成「宿主版本不支持」（中英）', async () => {
  const unsupported = () => ({ ok: false, error: { code: 'unknown_method' } });
  const zh = await loaded({ 'sources.scan': () => scan(), 'import.apply': unsupported });
  zh.page.byId('select-all').click();
  zh.page.byId('import').click();
  await settle();
  // 文案必须点明「宿主没有这个能力、要升级」，不能落到「未识别的原因：unknown_method」。
  assert.match(zh.page.byId('banner').textContent, /没有「导入 AI 供应商」能力/);
  assert.match(zh.page.byId('banner').textContent, /升级 GoNavi/);
  assert.doesNotMatch(zh.page.byId('banner').textContent, /未识别的原因/);
  assert.equal(zh.page.byId('import').disabled, false);

  const en = await loaded(
    { 'sources.scan': () => scan(), 'import.apply': unsupported },
    { prefs: { locale: 'en-US', theme: 'light' } },
  );
  en.page.byId('select-all').click();
  en.page.byId('import').click();
  await settle();
  assert.match(en.page.byId('banner').textContent, /no "import AI providers" capability/);
  assert.doesNotMatch(en.page.byId('banner').textContent, /Unrecognised reason/);
});

test('导入失败与宿主桥缺失都给出人话文案', async () => {
  const { page } = await loaded({
    'sources.scan': () => scan(),
    'import.apply': () => {
      throw new Error('后端进程已退出');
    },
  });
  page.byId('select-all').click();
  page.byId('import').click();
  await settle();
  assert.match(page.byId('banner').textContent, /导入失败：后端进程已退出/);
  assert.equal(page.byId('import').disabled, false);

  const noBridge = loadPage({});
  await settle();
  assert.deepEqual(noBridge.errors, []);
  assert.match(noBridge.byId('banner').textContent, /宿主桥不可用/);
});

test('英文宿主语言与主题按宿主偏好渲染', async () => {
  const { page } = await loaded({ 'sources.scan': () => scan() }, { prefs: { locale: 'en-US', theme: 'dark', palette: { accent: '#123456' } } });
  assert.equal(page.sandbox.document.documentElement.getAttribute('data-theme'), 'dark');
  assert.match(page.text('app-title'), /Import AI providers/);
  assert.match(page.text('selection'), /Nothing is selected yet/);
  assert.match(page.byId('sources').textContent, /Detected/);
  assert.equal(page.byId('sources').children[1].textContent.includes('entries'), true);
  assert.equal(page.sandbox.document.documentElement.style.props['--accent'], '#123456');

  page.firePreferences({ locale: 'zh-CN', theme: 'light' });
  assert.equal(page.sandbox.document.documentElement.getAttribute('data-theme'), 'light');
  assert.match(page.text('app-title'), /AI 配置导入/);
});

test('忽略缺少唯一标识或重复 sourceKey 的条目并如实说明', async () => {
  const { page } = await loaded({
    'sources.scan': () => ({
      sources: scan().sources,
      items: [item({ sourceKey: 'k1' }), item({ sourceKey: 'k1' }), { sourceId: 'cc-switch' }, item({ sourceKey: 'k2' })],
    }),
  });
  assert.equal(page.rows().length, 2);
  assert.match(page.byId('banner').textContent, /已忽略 2 条缺少唯一标识的条目/);
});

// ---- 筛选：只影响表格可见行，勾选跨筛选保持 ----

function bulkScan(ccCount = 100, magpieCount = 20) {
  const items = [];
  for (let index = 0; index < ccCount; index += 1) items.push(item({ sourceKey: 'c' + index, name: 'CC' + index }));
  for (let index = 0; index < magpieCount; index += 1) items.push(item({ sourceId: 'magpie', sourceKey: 'm' + index, name: 'MG' + index }));
  return { sources: scan().sources, items };
}

function chipOf(page, id) {
  return page.byId('filters').children.find((node) => node.getAttribute('data-source') === id);
}

function tick(page, index) {
  const box = rowBoxes(page)[index];
  box.checked = !box.checked;
  box.dispatch('change');
}

test('按软件筛选：单选、多选取并集、回到全部，勾选不随筛选丢失', async () => {
  const { page } = await loaded({ 'sources.scan': () => scan() });
  assert.equal(page.text('filters-label'), '按软件筛选：');
  assert.equal(page.byId('filters').children.length, 6, '全部 + 5 个已知来源（含未检测到的）各出 chip');
  assert.equal(page.byId('filters').children[0].textContent, '全部');
  assert.equal(page.byId('filters').children[0].getAttribute('data-on'), '1');
  assert.equal(chipOf(page, 'cc-switch').getAttribute('data-count'), '2');
  assert.equal(chipOf(page, 'magpie').getAttribute('data-count'), '1');
  assert.equal(chipOf(page, 'ccgui').getAttribute('data-count'), '0');
  assert.equal(chipOf(page, 'ccgui').getAttribute('data-empty'), '1', '未检测到的来源置灰且计 0');
  assert.match(page.text('visible'), /可见 3 \/ 共 3 条/);

  tick(page, 1);
  assert.match(page.text('selection'), /已选 1 条（跨页合计）/);

  chipOf(page, 'cc-switch').click();
  assert.equal(page.rows().length, 2);
  assert.match(page.text('visible'), /可见 2 \/ 共 3 条/);
  assert.equal(page.byId('filters').children[0].getAttribute('data-on'), '0', '有来源被选中时「全部」不再选中');

  chipOf(page, 'magpie').click();
  assert.equal(page.rows().length, 3, '多个来源取并集');
  assert.match(page.text('visible'), /可见 3 \/ 共 3 条/);

  page.byId('filters').children[0].click();
  assert.equal(page.rows().length, 3, '点「全部」清空筛选');
  assert.equal(page.byId('filters').children[0].getAttribute('data-on'), '1');

  chipOf(page, 'cc-switch').click();
  assert.equal(page.rows().length, 2);
  assert.equal(rowBoxes(page)[1].checked, true, '切筛选不许丢勾选');
  assert.match(page.text('selection'), /已选 1 条/);
});

test('筛选后 0 行给明确空态，不白屏不报错', async () => {
  const { page } = await loaded({ 'sources.scan': () => scan() });
  chipOf(page, 'ccgui').click();
  assert.equal(page.rows().length, 0);
  assert.match(page.byId('list').textContent, /当前筛选下没有条目/);
  assert.match(page.text('visible'), /可见 0 \/ 共 3 条/);
  assert.equal(page.byId('import').disabled, true);
  assert.equal(page.byId('select-all').disabled, true, '没有可见条目时全选没有作用对象');
  assert.match(page.text('page-total'), /共 0 条/);
  assert.deepEqual(page.errors, []);
  page.byId('filters').children[0].click();
  assert.equal(page.rows().length, 3);
});

test('全选/反选只作用于筛选后的条目，取消全选一键清空跨页勾选', async () => {
  const { page } = await loaded({ 'sources.scan': () => scan() });
  chipOf(page, 'magpie').click();
  assert.equal(page.text('select-all'), '全选（筛选后 1 条）');
  assert.match(page.byId('select-all').getAttribute('title'), /包含其它页/);
  assert.match(page.byId('invert').getAttribute('title'), /包含其它页/);
  assert.match(page.byId('clear-selection').getAttribute('title'), /清空全部勾选/);

  page.byId('select-all').click();
  assert.match(page.text('selection'), /已选 1 条/);
  page.byId('filters').children[0].click();
  assert.deepEqual(rowBoxes(page).map((box) => box.checked), [false, false, true], '全选不把筛选之外的行一起选上');

  page.byId('invert').click();
  assert.deepEqual(rowBoxes(page).map((box) => box.checked), [true, true, false]);
  assert.match(page.text('selection'), /已选 2 条/);

  page.byId('select-all').click();
  assert.match(page.text('selection'), /已选 3 条/);
  page.byId('clear-selection').click();
  assert.match(page.text('selection'), /还没有勾选条目/);
  assert.match(page.text('pager-selected'), /已勾选 0 条/);
  assert.equal(page.byId('import').disabled, true);
});

// ---- 分页 ----

test('分页：默认每页 50，首页/末页/上一页/下一页与每页条数都回到第 1 页', async () => {
  const { page } = await loaded({ 'sources.scan': () => bulkScan() });
  assert.equal(page.rows().length, 50, '默认每页 50 条');
  assert.match(page.text('page-info'), /第 1 \/ 3 页/);
  assert.match(page.text('page-total'), /共 120 条/);
  assert.equal(page.byId('page-prev').disabled, true);
  assert.equal(page.byId('page-first').disabled, true);
  assert.equal(page.byId('page-next').disabled, false);

  page.byId('page-next').click();
  assert.match(page.text('page-info'), /第 2 \/ 3 页/);
  assert.equal(page.rows().length, 50);
  assert.equal(page.byId('page-prev').disabled, false);
  page.byId('page-last').click();
  assert.match(page.text('page-info'), /第 3 \/ 3 页/);
  assert.equal(page.rows().length, 20, '末页只渲染剩下的 20 条');
  assert.equal(page.byId('page-next').disabled, true);
  assert.equal(page.byId('page-last').disabled, true);
  page.byId('page-first').click();
  assert.match(page.text('page-info'), /第 1 \/ 3 页/);

  const size = page.byId('page-size');
  assert.equal(size.value, '50');
  assert.deepEqual(size.children.map((node) => node.textContent), ['20', '50', '100', '200']);

  page.byId('page-last').click();
  size.value = '20';
  size.dispatch('change');
  assert.match(page.text('page-info'), /第 1 \/ 6 页/, '改每页条数回到第 1 页');
  assert.equal(page.rows().length, 20);

  size.value = '200';
  size.dispatch('change');
  assert.match(page.text('page-info'), /第 1 \/ 1 页/);
  assert.equal(page.rows().length, 120);
  assert.equal(page.byId('page-prev').disabled, true, '只有一页时分页栏保留、翻页按钮置灰');
  assert.equal(page.byId('page-next').disabled, true);

  size.value = '50';
  size.dispatch('change');
  page.byId('page-last').click();
  assert.match(page.text('page-info'), /第 3 \/ 3 页/);
  chipOf(page, 'magpie').click();
  assert.match(page.text('page-info'), /第 1 \/ 1 页/, '筛选变化回到第 1 页');
  assert.equal(page.rows().length, 20);
});

test('数据变少时当前页自动夹到末页，不出现空白页', async () => {
  let round = 0;
  const { page } = await loaded({
    'sources.scan': () => { round += 1; return round === 1 ? bulkScan() : scan(); },
  });
  page.byId('page-last').click();
  assert.match(page.text('page-info'), /第 3 \/ 3 页/);
  page.byId('rescan').click();
  await settle();
  assert.match(page.text('page-info'), /第 1 \/ 1 页/);
  assert.equal(page.rows().length, 3, '夹到末页而不是渲染空白页');
  assert.deepEqual(page.errors, []);
});

test('切页不丢勾选，已勾选条数跨页合计，超上限沿用既有拦截', async () => {
  const { page } = await loaded({ 'sources.scan': () => bulkScan() });
  tick(page, 0);
  assert.match(page.text('selection'), /已选 1 条（跨页合计）/);
  assert.match(page.text('pager-selected'), /已勾选 1 条（跨页合计）/);

  page.byId('page-next').click();
  assert.equal(rowBoxes(page)[0].checked, false, '下一页是另一批条目');
  tick(page, 0);
  assert.match(page.text('pager-selected'), /已勾选 2 条（跨页合计）/);

  page.byId('page-prev').click();
  assert.equal(rowBoxes(page)[0].checked, true, '切页不丢勾选');

  page.byId('select-all').click();
  assert.match(page.text('selection'), /已选 120 条/);
  assert.match(page.text('selection'), /一次最多导入 100 条/);
  assert.equal(page.byId('import').disabled, true);
  assert.match(page.text('pager-selected'), /已勾选 120 条/);

  page.byId('clear-selection').click();
  assert.match(page.text('selection'), /还没有勾选条目/);
  page.byId('page-next').click();
  assert.equal(rowBoxes(page)[0].checked, false, '取消全选清空其它页的勾选');
});

// ---- 两类跳过说明 ----

test('停用与跳过两条说明独立渲染，缺字段按 0，未知字段忽略', async () => {
  const { page } = await loaded({
    'sources.scan': () => ({
      sources: [
        { id: 'cc-switch', label: 'CC Switch', detected: true, path: 'x.db', count: 2, error: '', skipped: 3, futureField: 'ignore me' },
        { id: 'magpie', label: 'Magpie', detected: true, path: 'p.json', count: 1, error: '', disabled: 24, skipped: 7 },
      ],
      items: scan().items.concat([{ ...item({ sourceKey: 'k9', name: 'Extra' }), futureField: { deep: 1 } }]),
    }),
  });
  const cards = page.byId('sources').children;
  assert.deepEqual(cards[0].findAll('.source-note').map((node) => node.textContent), [
    '已跳过 3 条：源里没有可用的 http 端点、结构暂不支持，或与其它条目重复',
  ]);
  assert.deepEqual(cards[1].findAll('.source-note').map((node) => node.textContent), [
    '已停用 24 条（在源工具里关闭，启用后即可导入）',
    '已跳过 7 条：源里没有可用的 http 端点、结构暂不支持，或与其它条目重复',
  ]);
  assert.equal(page.rows().length, 4, '未知字段不影响条目渲染');

  const plain = await loaded({ 'sources.scan': () => scan() });
  const plainCards = plain.page.byId('sources').children;
  assert.deepEqual(plainCards[0].findAll('.source-note').map((node) => node.textContent), [], '缺字段不冒出说明文案');
  assert.deepEqual(plainCards[1].findAll('.source-note').map((node) => node.textContent), []);
});

test('英文宿主语言下的筛选、分页与两类跳过说明', async () => {
  const { page } = await loaded({
    'sources.scan': () => ({
      sources: [{ id: 'magpie', label: 'Magpie', detected: true, path: 'p.json', count: 1, error: '', disabled: 24, skipped: 7 }],
      items: [item({ sourceId: 'magpie', sourceKey: 'm1', name: 'Magpie One' })],
    }),
  }, { prefs: { locale: 'en-US', theme: 'dark' } });
  assert.equal(page.text('filters-label'), 'Filter by source:');
  assert.equal(page.byId('filters').children[0].textContent, 'All');
  assert.match(page.byId('filters').children[1].textContent, /^Magpie/);
  assert.deepEqual(page.byId('sources').children[0].findAll('.source-note').map((node) => node.textContent), [
    '24 entries are disabled upstream (turned off in the source tool); enable them there to import.',
    '7 entries were skipped: the source has no usable http endpoint, the shape is not supported yet, or the entry duplicates another.',
  ]);
  assert.match(page.text('visible'), /Showing 1 of 1/);
  assert.match(page.text('page-info'), /Page 1 \/ 1/);
  assert.match(page.text('page-total'), /1 total/);
  assert.match(page.text('pager-selected'), /0 selected \(across pages\)/);
  assert.match(page.text('select-all'), /Select all \(1 filtered\)/);
});

test('来源降级提示（notice）渲染为一行说明，既不置错误态也不隐藏条目数', async () => {
  const { page } = await loaded({
    'sources.scan': () => ({
      sources: [
        { id: 'cc-switch', label: 'CC Switch', detected: true, path: 'x.db', count: 2, error: '' },
        { id: 'magpie', label: 'Magpie', detected: true, path: 'p.json', count: 1, error: '', notice: '主配置不是合法 JSON，已回退到运行态文件', keyUnavailable: true },
      ],
      items: scan().items,
    }),
  });
  const cards = page.byId('sources').children;
  // 降级提示排在密钥提示之前，两条各自独立成行。
  assert.deepEqual(cards[1].findAll('.source-note').map((node) => node.textContent), [
    '主配置不是合法 JSON，已回退到运行态文件',
    '密钥需手工补填：该来源只暴露了掩码密钥（已退回只读元数据）。',
  ]);
  assert.match(cards[1].textContent, /1 条/, '降级仍显示条目数');
  assert.match(cards[1].textContent, /已检测到/, '降级不是错误态');
  assert.deepEqual(cards[0].findAll('.source-note').map((node) => node.textContent), [], '没有 notice 的来源不冒出说明');
});

test('宿主供应商列表取不到时给出「未标记冲突」提示，没标不等于不重复', async () => {
  const conflictUnavailable = await loaded({
    'sources.scan': () => ({ sources: scan().sources, items: scan().items, conflictUnavailable: true }),
  });
  assert.equal(conflictUnavailable.page.byId('banner').hidden, false);
  assert.match(conflictUnavailable.page.byId('banner').textContent, /没有标记「冲突」/);

  // 正常情况下（字段缺失或 false）不冒这条提示。
  const normal = await loaded({ 'sources.scan': () => scan() });
  assert.equal(normal.page.byId('banner').hidden, true);
});

test('source_read_failed（读不了）与 source_missing（配置被删）给不同文案', async () => {
  const { page } = await loaded({
    'sources.scan': () => scan(),
    'import.apply': () => ({
      imported: 0,
      skipped: 0,
      failed: 2,
      resolved: 0,
      items: [
        { index: 0, name: '', status: 'failed', providerId: '', reason: 'source_read_failed' },
        { index: 1, name: '', status: 'failed', providerId: '', reason: 'source_missing' },
      ],
    }),
  });
  page.byId('select-all').click();
  page.byId('import').click();
  await settle();
  const rows = page.byId('result').findAll('.res-row');
  assert.match(rows[0].textContent, /读取这条来源配置失败/);
  assert.match(rows[1].textContent, /来源里已经找不到这条配置/);
});

