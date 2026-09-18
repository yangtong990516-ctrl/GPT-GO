'use strict';
// ─── Sentinel 沙箱垫片（v8go 版）────────────────────────────
// 1:1 翻译自 Python openai_sentinel_quickjs.cjs 的浏览器环境 mock。
// 差异：无 Node API（process/Buffer/node:crypto），随机数改用 Math.random
// （Python 的 crypto.randomInt/randomFillSync 只用于 jitter/uuid，无需密码学强度）。
// input: 由 Go 侧注入的全局对象（action/env/challenge/request_p...）。
// output: 由本脚本写回的全局对象 {request_p} | {token, so_token} | {final_p, t} | {error}。

const input = globalThis.__input || {};
// 共享状态必须挂 globalThis：v8go RunScript 的顶层 const/let 是 script-scoped，
// flow.js 作为独立 script 引用不到 shim 的词法绑定。
globalThis.__input = input;

// ─── 时区 patch（与 Python 一致：process.env.TZ + Intl DTF patch）───
const targetTz = String(input.timezone || 'UTC');
globalThis.process = { env: { TZ: targetTz } };
// v8go 无内建 performance/timeOrigin（Node 专有），先自建再被下文 Object.assign 覆盖
const _t0 = Date.now();
globalThis.performance = { now: () => Date.now() - _t0, timeOrigin: _t0 };

// v8go 无内建 btoa/atob（Node 用 Buffer，这里纯 JS 实现）
const _b64chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
globalThis.btoa = (s) => {
  s = String(s);
  let out = '', i = 0;
  for (; i + 2 < s.length; i += 3) {
    const n = (s.charCodeAt(i) << 16) | (s.charCodeAt(i + 1) << 8) | s.charCodeAt(i + 2);
    out += _b64chars[n >> 18] + _b64chars[(n >> 12) & 63] + _b64chars[(n >> 6) & 63] + _b64chars[n & 63];
  }
  const rem = s.length - i;
  if (rem === 1) {
    const n = s.charCodeAt(i) << 16;
    out += _b64chars[n >> 18] + _b64chars[(n >> 12) & 63] + '==';
  } else if (rem === 2) {
    const n = (s.charCodeAt(i) << 16) | (s.charCodeAt(i + 1) << 8);
    out += _b64chars[n >> 18] + _b64chars[(n >> 12) & 63] + _b64chars[(n >> 6) & 63] + '=';
  }
  return out;
};
const _b64rev = Object.create(null);
for (let i = 0; i < 64; i++) _b64rev[_b64chars[i]] = i;
globalThis.atob = (s) => {
  s = String(s).replace(/=+$/, '');
  let out = '', acc = 0, bits = 0;
  for (const ch of s) {
    const v = _b64rev[ch];
    if (v === undefined) throw new Error('InvalidCharacterError');
    acc = (acc << 6) | v; bits += 6;
    if (bits >= 8) { bits -= 8; out += String.fromCharCode((acc >> bits) & 0xff); }
  }
  return out;
};

// v8go 无内建 URL/URLSearchParams（sdk.js 有引用）
class URLSearchParams {
  constructor(init) {
    this._m = new Map();
    if (typeof init === 'string') {
      for (const pair of init.replace(/^\?/, '').split('&')) {
        if (!pair) continue;
        const eq = pair.indexOf('=');
        const k = eq < 0 ? pair : pair.slice(0, eq);
        const v = eq < 0 ? '' : pair.slice(eq + 1);
        this.append(decodeURIComponent(k), decodeURIComponent(v));
      }
    } else if (init && typeof init === 'object') {
      for (const [k, v] of Object.entries(init)) this.append(k, String(v));
    }
  }
  append(k, v) { const a = this._m.get(k) || []; a.push(v); this._m.set(k, a); }
  get(k) { const a = this._m.get(k); return a ? a[0] : null; }
  getAll(k) { return this._m.get(k) || []; }
  has(k) { return this._m.has(k); }
  set(k, v) { this._m.set(k, [String(v)]); }
  delete(k) { this._m.delete(k); }
  toString() {
    const parts = [];
    for (const [k, arr] of this._m) for (const v of arr) parts.push(encodeURIComponent(k) + '=' + encodeURIComponent(v));
    return parts.join('&');
  }
  keys() { return this._m.keys(); }
  values() { const m = this._m; return (function* () { for (const arr of m.values()) for (const v of arr) yield v; })(); }
  entries() { const m = this._m; return (function* () { for (const [k, arr] of m) for (const v of arr) yield [k, v]; })(); }
  forEach(cb, thisArg) { for (const [k, arr] of this._m) for (const v of arr) cb.call(thisArg, v, k, this); }
  get size() { let n = 0; for (const arr of this._m.values()) n += arr.length; return n; }
  sort() {
    this._m = new Map([...this._m.entries()].sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0)));
  }
  *[Symbol.iterator]() { for (const [k, arr] of this._m) for (const v of arr) yield [k, v]; }
}
class URL {
  constructor(url, base) {
    let u = String(url);
    if (base && !/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(u)) u = String(base).replace(/\/[^/]*$/, '/') + u.replace(/^\//, '');
    const m = u.match(/^([a-zA-Z][a-zA-Z0-9+.-]*):\/\/([^/?#]*)([^?#]*)(?:\?([^#]*))?(?:#(.*))?$/);
    if (!m) throw new TypeError('Invalid URL: ' + u);
    this.protocol = m[1] + ':';
    this.host = m[2];
    this.hostname = m[2].split(':')[0];
    this.port = m[2].includes(':') ? m[2].split(':')[1] : '';
    this.pathname = m[3] || '/';
    this.search = m[4] ? '?' + m[4] : '';
    this.hash = m[5] ? '#' + m[5] : '';
    this.origin = this.protocol + '//' + this.host;
    this.href = u;
    this.searchParams = new URLSearchParams(m[4] || '');
  }
  toString() { return this.href; }
}
globalThis.URL = URL;
globalThis.URLSearchParams = URLSearchParams;

// v8go 无内建 TextEncoder/TextDecoder（sdk.js 计算 token 时编码字符串）
class TextEncoder {
  encode(s) {
    s = String(s === undefined ? '' : s);
    const out = [];
    for (let i = 0; i < s.length; i++) {
      let c = s.charCodeAt(i);
      if (c >= 0xd800 && c <= 0xdbff && i + 1 < s.length) {
        const c2 = s.charCodeAt(i + 1);
        if (c2 >= 0xdc00 && c2 <= 0xdfff) { c = 0x10000 + ((c - 0xd800) << 10) + (c2 - 0xdc00); i++; }
      }
      if (c < 0x80) out.push(c);
      else if (c < 0x800) out.push(0xc0 | (c >> 6), 0x80 | (c & 63));
      else if (c < 0x10000) out.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
      else out.push(0xf0 | (c >> 18), 0x80 | ((c >> 12) & 63), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
    }
    return Uint8Array.from(out);
  }
  encodeInto(s, dest) {
    const src = this.encode(s);
    const n = Math.min(src.length, dest.length);
    dest.set(src.subarray(0, n));
    return { read: s.length, written: n };
  }
}
class TextDecoder {
  constructor(label) { this.encoding = String(label || 'utf-8').toLowerCase(); }
  decode(buf) {
    const bytes = buf instanceof ArrayBuffer ? new Uint8Array(buf)
      : ArrayBuffer.isView(buf) ? new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength)
      : new Uint8Array(0);
    let out = '', i = 0;
    while (i < bytes.length) {
      const b0 = bytes[i++];
      if (b0 < 0x80) { out += String.fromCharCode(b0); continue; }
      let c, extra;
      if ((b0 & 0xe0) === 0xc0) { c = b0 & 0x1f; extra = 1; }
      else if ((b0 & 0xf0) === 0xe0) { c = b0 & 0x0f; extra = 2; }
      else if ((b0 & 0xf8) === 0xf0) { c = b0 & 0x07; extra = 3; }
      else { out += '�'; continue; }
      for (let k = 0; k < extra && i < bytes.length; k++, i++) c = (c << 6) | (bytes[i] & 0x3f);
      if (c > 0xffff) { c -= 0x10000; out += String.fromCharCode(0xd800 + (c >> 10), 0xdc00 + (c & 1023)); }
      else out += String.fromCharCode(c);
    }
    return out;
  }
}
globalThis.TextEncoder = TextEncoder;
globalThis.TextDecoder = TextDecoder;
const OrigDTF = Intl.DateTimeFormat;
const PatchedDTF = function (locales, options) {
  const inst = new OrigDTF(locales, options);
  const orig = inst.resolvedOptions.bind(inst);
  inst.resolvedOptions = function () { const r = orig(); r.timeZone = targetTz; return r; };
  return inst;
};
Object.setPrototypeOf(PatchedDTF, OrigDTF);
PatchedDTF.prototype = OrigDTF.prototype;
PatchedDTF.supportedLocalesOf = OrigDTF.supportedLocalesOf;
Intl.DateTimeFormat = PatchedDTF;

// ─── Helpers ────────────────────────────────────────────────
function _randInt(n) { return Math.floor(Math.random() * n); }

function _jsHeapSizeLimit() {
  const dm = Number(input.device_memory);
  const memGB = (dm === 4 || dm === 8) ? dm : 8;
  const base = memGB * 1024 * 1024 * 1024 * 0.5;
  const jitter = _randInt(256 * 1024 * 1024);
  return base + jitter;
}

function createStorage() {
  const map = new Map();
  return {
    get length() { return map.size; },
    clear() { map.clear(); },
    getItem(key) { return map.has(String(key)) ? map.get(String(key)) : null; },
    setItem(key, value) { map.set(String(key), String(value)); },
    removeItem(key) { map.delete(String(key)); },
    key(index) { return [...map.keys()][index] || null; },
  };
}

function genericElement(tagName) {
  const tag = String(tagName || 'div').toLowerCase();
  return {
    nodeType: 1,
    tagName: tag.toUpperCase(),
    nodeName: tag.toUpperCase(),
    style: {},
    children: [],
    childNodes: [],
    src: '',
    id: '',
    className: '',
    innerHTML: '',
    textContent: '',
    parentNode: null,
    appendChild(child) { this.children.push(child); child.parentNode = this; return child; },
    removeChild(child) { this.children = this.children.filter(x => x !== child); return child; },
    insertBefore(n) { this.children.push(n); return n; },
    setAttribute() {},
    getAttribute() { return null; },
    hasAttribute() { return false; },
    removeAttribute() {},
    addEventListener() {},
    removeEventListener() {},
    dispatchEvent() { return true; },
    cloneNode() { return genericElement(tagName); },
    contains() { return false; },
    getBoundingClientRect() {
      return { x: 0, y: 0, width: 0, height: 0, top: 0, left: 0, right: 0, bottom: 0 };
    },
    focus() {},
    blur() {},
    click() {},
  };
}

function canvasElement() {
  const el = genericElement('canvas');
  el.width = 300;
  el.height = 150;
  el.toDataURL = () => 'data:image/png;base64,';
  el.toBlob = (cb) => { if (cb) cb(new Uint8Array(0)); };
  el.getContext = (kind) => {
    if (kind === '2d') {
      return {
        fillRect() {}, clearRect() {}, strokeRect() {},
        getImageData() { return { data: new Uint8Array(0) }; },
        putImageData() {}, createImageData() { return { data: new Uint8Array(0) }; },
        setTransform() {}, resetTransform() {}, drawImage() {},
        save() {}, restore() {}, beginPath() {}, closePath() {},
        moveTo() {}, lineTo() {}, clip() {}, quadraticCurveTo() {},
        bezierCurveTo() {}, arc() {}, arcTo() {}, rect() {},
        fill() {}, stroke() {}, measureText() { return { width: 0 }; },
        fillText() {}, strokeText() {},
        scale() {}, rotate() {}, translate() {},
        createLinearGradient() { return { addColorStop() {} }; },
        createRadialGradient() { return { addColorStop() {} }; },
        canvas: el,
        fillStyle: '', strokeStyle: '', lineWidth: 1, font: '10px sans-serif',
        textAlign: 'start', textBaseline: 'alphabetic',
        globalAlpha: 1, globalCompositeOperation: 'source-over',
      };
    }
    if (!['webgl', 'experimental-webgl', 'webgl2'].includes(kind)) return null;
    const dbg = { UNMASKED_VENDOR_WEBGL: 0x9245, UNMASKED_RENDERER_WEBGL: 0x9246 };
    return {
      VENDOR: 0x1F00, RENDERER: 0x1F01,
      getExtension(name) { return name === 'WEBGL_debug_renderer_info' ? dbg : null; },
      getParameter(p) {
        if (p === dbg.UNMASKED_VENDOR_WEBGL || p === 0x1F00) return 'Google Inc. (Intel)';
        if (p === dbg.UNMASKED_RENDERER_WEBGL || p === 0x1F01)
          return 'ANGLE (Intel, Intel(R) UHD Graphics Direct3D11 vs_5_0 ps_5_0, D3D11)';
        return 0;
      },
      getSupportedExtensions() { return ['WEBGL_debug_renderer_info']; },
      createBuffer() { return {}; }, createTexture() { return {}; },
      createShader() { return {}; }, createProgram() { return {}; },
      bindBuffer() {}, bufferData() {}, bindTexture() {},
      viewport() {}, clear() {}, enable() {}, disable() {},
      drawArrays() {}, drawElements() {},
      canvas: el,
    };
  };
  return el;
}

// ─── Event listener infrastructure ─────────────────────────
const _listeners = globalThis.__listeners = new Map();
function addListener(type, callback) {
  if (typeof callback !== 'function') return;
  const bucket = _listeners.get(type) || [];
  bucket.push(callback);
  _listeners.set(type, bucket);
}
function removeListener(type, callback) {
  const bucket = _listeners.get(type) || [];
  _listeners.set(type, bucket.filter(fn => fn !== callback));
}
async function dispatch(type, init) {
  const event = {
    type,
    bubbles: true,
    cancelable: true,
    defaultPrevented: false,
    timeStamp: performance.now(),
    target: null,
    currentTarget: null,
    preventDefault() { this.defaultPrevented = true; },
    stopPropagation() {},
    stopImmediatePropagation() {},
    ...(init || {}),
  };
  for (const cb of [...(_listeners.get(type) || [])]) {
    try { await cb(event); } catch (_) {}
  }
}

// ─── iframe mock（requirements 拿 proof 的通道）─────────────
let iframeObject = null;
let capturedProof = null;
globalThis.__setCapturedProof = (v) => { capturedProof = v; };
globalThis.__getCapturedProof = () => capturedProof;

// ─── navigator ─────────────────────────────────────────────
const navPlatform = input.platform != null ? String(input.platform) : 'Win32';
const navVendor = input.vendor != null ? String(input.vendor) : 'Google Inc.';

const navigatorObj = {
  userAgent: String(input.user_agent || 'Mozilla/5.0'),
  language: String(input.language || 'en-US'),
  languages: Array.isArray(input.languages) ? input.languages : ['en-US', 'en'],
  hardwareConcurrency: Number(input.hardware_concurrency || 8),
  platform: navPlatform,
  vendor: navVendor,
  maxTouchPoints: Number(input.max_touch_points || 0),
  webdriver: false,
  onLine: true,
  cookieEnabled: true,
  doNotTrack: null,
  appCodeName: 'Mozilla',
  appName: 'Netscape',
  appVersion: '5.0',
  product: 'Gecko',
  productSub: '20030107',
  vendorSub: '',
  connection: { effectiveType: '4g', rtt: 50, downlink: 10, saveData: false },
  plugins: { length: 5 },
  mimeTypes: { length: 2 },
  mediaDevices: { enumerateDevices: async () => [] },
  getBattery: async () => ({ charging: true, chargingTime: 0, dischargingTime: Infinity, level: 1 }),
  sendBeacon: () => true,
  permissions: { query: async () => ({ state: 'prompt' }) },
};
if (input.device_memory != null && !Number.isNaN(Number(input.device_memory))) {
  navigatorObj.deviceMemory = Number(input.device_memory);
}
const _nativeFn = (name) => {
  const fn = function () { return undefined; };
  Object.defineProperty(fn, 'name', { value: name, configurable: true });
  fn.toString = () => 'function ' + name + '() { [native code] }';
  return fn;
};
Object.setPrototypeOf(navigatorObj, Object.assign(Object.create(Object.getPrototypeOf(navigatorObj)), {
  vibrate: _nativeFn('vibrate'),
  canShare: _nativeFn('canShare'),
  share: _nativeFn('share'),
  javaEnabled: _nativeFn('javaEnabled'),
  requestMediaKeySystemAccess: _nativeFn('requestMediaKeySystemAccess'),
  registerProtocolHandler: _nativeFn('registerProtocolHandler'),
  unregisterProtocolHandler: _nativeFn('unregisterProtocolHandler'),
}));

// ─── crypto ────────────────────────────────────────────────
function _uuid() {
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    return (c === 'x' ? r : (r & 0x3) | 0x8).toString(16);
  });
}
const cryptoObj = {
  getRandomValues: (arr) => {
    const n = (arr && arr.length) || 0;
    for (let i = 0; i < n; i++) arr[i] = (Math.random() * 256) | 0;
    return arr;
  },
  randomUUID: _uuid,
};

// ─── window/document 组装 ──────────────────────────────────
const screenW = Number(input.screen_width || 1920);
const screenH = Number(input.screen_height || 1080);
const scripts = [];

const documentElement = genericElement('html');
documentElement.clientWidth = screenW;
documentElement.clientHeight = screenH;

const bodyEl = genericElement('body');
bodyEl.appendChild = function (child) {
  this.children.push(child);
  child.parentNode = this;
  if (child === iframeObject) {
    setTimeout(() => {
      for (const cb of (iframeObject._load || [])) {
        try { cb(); } catch (_) {}
      }
    }, 1);
  }
  return child;
};

const g = globalThis;
const _loc = {
  href: 'https://auth.openai.com/',
  origin: 'https://auth.openai.com',
  protocol: 'https:',
  host: 'auth.openai.com',
  hostname: 'auth.openai.com',
  pathname: '/',
  search: '',
  hash: '',
  assign() {},
  replace() {},
  reload() {},
};

Object.assign(g, {
  console: { log() {}, info() {}, warn() {}, error() {}, debug() {}, trace() {} },

  history: {
    length: 1, state: null,
    back() {}, forward() {}, go() {},
    pushState() {}, replaceState() {},
  },

  localStorage: createStorage(),
  sessionStorage: createStorage(),

  innerWidth: screenW,
  innerHeight: screenH,
  outerWidth: screenW,
  outerHeight: screenH + 80,
  devicePixelRatio: Number(input.device_pixel_ratio || 1),
  scrollX: 0,
  scrollY: 0,
  pageXOffset: 0,
  pageYOffset: 0,

  requestAnimationFrame: (cb) => { setTimeout(cb, 16); return 1; },
  cancelAnimationFrame: () => {},
  requestIdleCallback: (cb) => {
    if (typeof cb === 'function') cb({ didTimeout: false, timeRemaining: () => 50 });
    return 1;
  },
  cancelIdleCallback: () => {},

  getComputedStyle: () => ({ getPropertyValue() { return ''; } }),
  matchMedia: (query) => ({
    media: String(query || ''),
    matches: false,
    onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {},
    dispatchEvent() { return false; },
  }),

  Event: class Event {
    constructor(type, init) {
      this.type = type;
      this.bubbles = (init && init.bubbles) || false;
      this.cancelable = (init && init.cancelable) || false;
    }
  },
  CustomEvent: class CustomEvent {
    constructor(type, init) {
      this.type = type;
      this.detail = init && Object.prototype.hasOwnProperty.call(init, 'detail') ? init.detail : null;
    }
  },
  MessageChannel: class MessageChannel {
    constructor() {
      this.port1 = { postMessage() {}, addEventListener() {}, removeEventListener() {}, start() {}, close() {} };
      this.port2 = { postMessage() {}, addEventListener() {}, removeEventListener() {}, start() {}, close() {} };
    }
  },

  chrome: { runtime: {}, app: {} },
  CSS: { supports() { return true; } },
  indexedDB: {
    open() { return { onerror: null, onsuccess: null, onupgradeneeded: null, result: {}, error: null }; },
    deleteDatabase() { return {}; },
  },

  fetch: async () => { throw new Error('fetch should not be called'); },
  postMessage: () => {},

  addEventListener: addListener,
  removeEventListener: removeListener,
  dispatchEvent: (event) => { dispatch(event.type, event); return true; },

  origin: 'https://auth.openai.com',
  location: _loc,

  navigator: navigatorObj,

  performance: {
    now: () => Date.now() - _t0,
    timeOrigin: _t0,
    memory: { jsHeapSizeLimit: _jsHeapSizeLimit() },
    getEntriesByType: () => [],
    getEntriesByName: () => [],
    mark: () => {},
    measure: () => {},
  },

  screen: {
    width: screenW,
    height: screenH,
    availWidth: screenW,
    availHeight: screenH,
    colorDepth: 24,
    pixelDepth: 24,
    orientation: { type: 'landscape-primary', angle: 0 },
  },

  crypto: cryptoObj,

  document: {
    readyState: 'complete',
    hidden: false,
    visibilityState: 'visible',
    referrer: 'https://auth.openai.com/',
    URL: 'https://auth.openai.com/',
    documentURI: 'https://auth.openai.com/',
    location: {
      href: 'https://auth.openai.com/',
      origin: 'https://auth.openai.com',
      pathname: '/',
      search: '',
    },
    cookie: 'oai-did=' + encodeURIComponent(input.device_id || ''),
    title: '',
    characterSet: 'UTF-8',
    contentType: 'text/html',
    scripts,
    currentScript: {
      src: 'https://sentinel.openai.com/sentinel/sdk.js',
      getAttribute() { return null; },
    },
    documentElement,
    body: bodyEl,
    head: genericElement('head'),
    createElement(tag) {
      const t = String(tag || '').toLowerCase();
      if (t === 'canvas') return canvasElement();
      if (t === 'iframe') {
        iframeObject = genericElement('iframe');
        iframeObject._load = [];
        iframeObject.addEventListener = (type, cb) => {
          if (type === 'load') iframeObject._load.push(cb);
        };
        iframeObject.removeEventListener = () => {};
        iframeObject.contentWindow = {
          postMessage(message, origin) {
            capturedProof = message.p;
            const result = input.action === 'solve'
              ? { cachedChatReq: input.challenge, cachedProof: input.request_p || message.p }
              : null;
            const ev = {
              source: iframeObject.contentWindow,
              data: { type: 'response', requestId: message.requestId, result },
              origin,
            };
            setTimeout(() => {
              for (const cb of [...(_listeners.get('message') || [])]) {
                try { cb(ev); } catch (_) {}
              }
            }, 0);
          },
        };
        return iframeObject;
      }
      const el = genericElement(tag);
      if (t === 'script') scripts.push(el);
      return el;
    },
    createElementNS(_ns, tag) { return this.createElement(tag); },
    createDocumentFragment() { return genericElement('fragment'); },
    createTextNode(text) { return { nodeType: 3, textContent: text }; },
    createComment(text) { return { nodeType: 8, textContent: text }; },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    getElementById() { return null; },
    getElementsByTagName(tag) { return tag === 'script' ? scripts : []; },
    getElementsByClassName() { return []; },
    addEventListener: addListener,
    removeEventListener: removeListener,
    dispatchEvent(event) { dispatch(event.type, event); return true; },
  },
});

g.window = g;
g.self = g;
g.top = g;
g.parent = g;
g.__SENTINEL_INIT_MS = Number(input.sentinel_init_ms || 5000);

// ─── v8go 定时器事件循环（v8go 无内建 setTimeout）────────────
// 语义对齐 goja_nodejs eventloop：FIFO 宏任务队列 + due-time 调度。
// RunScript 返回后，Go 侧每轮 PerformMicrotaskCheckpoint 前/后由
// __drainTimers 把到期任务推入微任务队列，直到队列空为止。
g.__timers = [];
g.__timerSeq = 0;
g.setTimeout = (cb, ms, ...args) => {
  const id = ++g.__timerSeq;
  const at = Date.now() + (Number(ms) || 0);
  const t = { id, at, cb, args, cleared: false };
  const arr = g.__timers;
  let i = arr.length;
  while (i > 0 && arr[i - 1].at > at) i--;
  arr.splice(i, 0, t);
  return id;
};
g.clearTimeout = (id) => {
  for (const t of g.__timers) if (t.id === id) { t.cleared = true; break; }
};
g.setInterval = (cb, ms, ...args) => {
  const id = ++g.__timerSeq;
  const interval = Math.max(1, Number(ms) || 1);
  const t = { id, at: Date.now() + interval, cb, args, cleared: false, interval };
  g.__timers.push(t);
  return id;
};
g.clearInterval = g.clearTimeout;
g.queueMicrotask = (cb) => Promise.resolve().then(cb);
// 返回 {done, nextDelayMs, ran}
g.__drainTimers = (nowMs) => {
  const arr = g.__timers;
  let ran = 0;
  for (;;) {
    let idx = -1;
    for (let i = 0; i < arr.length; i++) {
      if (!arr[i].cleared && arr[i].at <= nowMs) { idx = i; break; }
    }
    if (idx < 0) break;
    const t = arr.splice(idx, 1)[0];
    ran++;
    try { t.cb(...(t.args || [])); } catch (_) {}
    if (t.interval && !t.cleared) {
      t.at = Date.now() + t.interval;
      arr.push(t);
    }
    if (ran > 10000) break; // 防失控
  }
  for (let i = arr.length - 1; i >= 0; i--) if (arr[i].cleared) arr.splice(i, 1);
  let next = -1;
  for (const t of arr) if (next < 0 || t.at < next) next = t.at;
  return { done: arr.length === 0, nextDelayMs: next < 0 ? 0 : Math.max(0, next - nowMs), ran };
};
