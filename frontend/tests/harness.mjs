// 插件页测试骨架：把 frontend/index.html 的脚本块按页面顺序在 vm 里跑一遍，
// 并提供一个刚好覆盖本页 DOM 用法的迷你实现。存在的意义是让「页面逻辑」不依赖浏览器也能断言，
// 浏览器里的真实渲染与截图由 tests/browser-verify.py 负责。
import fs from 'node:fs';
import path from 'node:path';
import url from 'node:url';
import vm from 'node:vm';

const root = path.resolve(path.dirname(url.fileURLToPath(import.meta.url)), '..');

export function readPage() {
  return fs.readFileSync(path.join(root, 'index.html'), 'utf8');
}

export function extractScripts(html) {
  const scripts = [];
  const pattern = /<script>([\s\S]*?)<\/script>/g;
  let match;
  while ((match = pattern.exec(html)) !== null) scripts.push(match[1]);
  return scripts;
}

export function extractIds(html) {
  const ids = [];
  const pattern = /<(\w+)([^>]*?)\sid="([^"]+)"/g;
  let match;
  while ((match = pattern.exec(html)) !== null) ids.push({ tag: match[1], id: match[3], attrs: match[2] });
  return ids;
}

class MiniNode {
  constructor(tagName) {
    this.tagName = String(tagName).toUpperCase();
    this.childNodes = [];
    this.attributes = {};
    this.listeners = new Map();
    this.className = '';
    this.text = '';
    this.checked = false;
    this.disabled = false;
    this.hidden = false;
    this.type = '';
    this.value = '';
    this.parentNode = null;
    this.style = { props: {}, setProperty(name, value) { this.props[name] = value; } };
  }

  get children() { return this.childNodes.filter((node) => node instanceof MiniNode); }

  appendChild(node) {
    if (node.parentNode) node.parentNode.removeChild(node);
    node.parentNode = this;
    this.text = '';
    this.childNodes.push(node);
    return node;
  }

  append(...nodes) { nodes.forEach((node) => this.appendChild(node)); }

  replaceChildren(...nodes) {
    this.childNodes = [];
    this.text = '';
    nodes.forEach((node) => this.appendChild(node));
  }

  removeChild(node) {
    const index = this.childNodes.indexOf(node);
    if (index >= 0) this.childNodes.splice(index, 1);
    node.parentNode = null;
    return node;
  }

  remove() { if (this.parentNode) this.parentNode.removeChild(this); }

  setAttribute(name, value) { this.attributes[name] = String(value); }
  getAttribute(name) { return Object.prototype.hasOwnProperty.call(this.attributes, name) ? this.attributes[name] : null; }
  hasAttribute(name) { return Object.prototype.hasOwnProperty.call(this.attributes, name); }

  addEventListener(type, handler) {
    const list = this.listeners.get(type) || [];
    list.push(handler);
    this.listeners.set(type, list);
  }

  removeEventListener(type, handler) {
    const list = this.listeners.get(type) || [];
    this.listeners.set(type, list.filter((item) => item !== handler));
  }

  dispatch(type, event) {
    const detail = event || { type, target: this };
    for (const handler of this.listeners.get(type) || []) handler.call(this, detail);
  }

  click() { this.dispatch('click'); }

  // 只支持测试真正用到的选择器：tag、.class、#id、[attr]、[attr="value"]
  matches(selector) {
    return String(selector).split(',').some((raw) => {
      const part = raw.trim();
      const attr = /^\[([\w-]+)(?:="?([^"\]]*)"?)?\]$/.exec(part);
      if (attr) {
        const value = this.attributes[attr[1]];
        if (value === undefined) return false;
        return attr[2] === undefined ? true : value === attr[2];
      }
      if (part.startsWith('#')) return this.attributes.id === part.slice(1);
      if (part.startsWith('.')) return String(this.className).split(/\s+/).includes(part.slice(1));
      return this.tagName === part.toUpperCase();
    });
  }

  findAll(selector, out = []) {
    for (const child of this.childNodes) {
      if (!(child instanceof MiniNode)) continue;
      if (child.matches(selector)) out.push(child);
      child.findAll(selector, out);
    }
    return out;
  }

  querySelector(selector) { return this.findAll(selector)[0] || null; }
  querySelectorAll(selector) { return this.findAll(selector); }

  get firstChild() { return this.childNodes[0] || null; }

  get textContent() {
    if (this.childNodes.length) return this.childNodes.map((node) => node.textContent || '').join('');
    return this.text;
  }

  set textContent(value) {
    this.childNodes = [];
    this.text = value === null || value === undefined ? '' : String(value);
  }
}

function createDocument(html) {
  const document = new MiniNode('#document');
  document.readyState = 'complete';
  document.title = '';
  document.documentElement = new MiniNode('html');
  document.body = new MiniNode('body');
  document.appendChild(document.documentElement);
  document.appendChild(document.body);
  document.createElement = (tag) => new MiniNode(tag);
  document.createTextNode = (text) => {
    const node = new MiniNode('#text');
    node.text = String(text);
    return node;
  };
  // 页面静态骨架里的元素没有 HTML 解析器可用，按 id 逐个补出来，
  // 保证 getElementById 与容器遍历的行为和浏览器一致。
  for (const { tag, id } of extractIds(html)) {
    const node = new MiniNode(tag);
    node.setAttribute('id', id);
    document.body.appendChild(node);
  }
  document.getElementById = (id) => document.body.findAll('[id]').find((node) => node.getAttribute('id') === id) || null;
  return document;
}

export function loadPage(options = {}) {
  const html = options.html || readPage();
  const document = createDocument(html);
  const errors = [];
  const windowListeners = new Map();

  const sandbox = {
    document,
    console,
    setTimeout,
    clearTimeout,
    Promise,
    JSON,
    Math,
    Date,
    gonavi: options.bridge,
    gonaviPlugin: options.windowBridge,
    gonaviHostPrefs: options.prefs,
    navigator: {},
    addEventListener(type, handler) {
      const list = windowListeners.get(type) || [];
      list.push(handler);
      windowListeners.set(type, list);
    },
    removeEventListener(type, handler) {
      const list = windowListeners.get(type) || [];
      windowListeners.set(type, list.filter((item) => item !== handler));
    },
    dispatchEvent(type) {
      for (const handler of windowListeners.get(type) || []) handler({ type });
    },
  };
  sandbox.globalThis = sandbox;
  sandbox.self = sandbox;
  sandbox.window = sandbox;
  const context = vm.createContext(sandbox);
  for (const [index, source] of extractScripts(html).entries()) {
    try {
      vm.runInContext(source, context, { filename: 'page-script-' + index + '.js' });
    } catch (error) {
      errors.push({ block: index, message: String(error && error.message ? error.message : error) });
    }
  }
  return {
    document,
    sandbox,
    errors,
    byId: (id) => document.getElementById(id),
    text: (id) => {
      const node = document.getElementById(id);
      return node ? node.textContent : '';
    },
    rows: () => document.body.querySelectorAll('tr').filter((node) => String(node.className) === 'row'),
    firePreferences: (prefs) => {
      sandbox.gonaviHostPrefs = prefs;
      sandbox.dispatchEvent('gonavi:host-preferences');
    },
  };
}

export async function settle(rounds = 3) {
  for (let index = 0; index < rounds; index += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
}

// 造一个按命令分派的宿主桥；每个命令返回一个 Promise 或直接抛错。
export function makeBridge(handlers) {
  const calls = [];
  return {
    calls,
    call(method, args) {
      calls.push({ method, command: args && args.command, args });
      if (method !== 'plugin.invoke') return Promise.reject(new Error('unexpected method ' + method));
      const handler = handlers[args && args.command];
      if (!handler) return Promise.reject(new Error('unexpected command ' + (args && args.command)));
      try {
        return Promise.resolve(handler(args.arguments || {}));
      } catch (error) {
        return Promise.reject(error);
      }
    },
    notify(message, type) { calls.push({ method: 'ui.notify', message, type }); },
  };
}

export function item(overrides = {}) {
  return {
    sourceId: 'cc-switch',
    sourceKey: 'claude:' + (overrides.sourceKey || 'k1'),
    name: 'Provider',
    baseUrl: 'https://api.example.com',
    apiFormat: 'anthropic',
    model: 'claude-opus-5',
    models: ['claude-opus-5'],
    hasKey: true,
    appType: 'claude',
    current: false,
    conflict: false,
    ...overrides,
  };
}

export function scan(sourceOverrides = {}) {
  return {
    sources: [
      { id: 'cc-switch', label: 'CC Switch', detected: true, path: 'C:\\Users\\cky\\.cc-switch\\cc-switch.db', count: 2, error: '', ...(sourceOverrides['cc-switch'] || {}) },
      { id: 'magpie', label: 'Magpie', detected: true, path: 'C:\\Users\\cky\\.cache\\magpie\\providers-state.json', count: 1, error: '', ...(sourceOverrides.magpie || {}) },
    ],
    items: [
      item({ sourceKey: 'k1', name: '千刀' }),
      item({ sourceKey: 'k2', name: '百倍', conflict: true }),
      item({ sourceId: 'magpie', sourceKey: 'm1', name: 'Magpie One', hasKey: false, appType: 'codex' }),
    ],
  };
}
