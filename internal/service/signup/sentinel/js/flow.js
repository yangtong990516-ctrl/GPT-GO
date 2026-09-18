'use strict';
// ─── 两阶段求解主流程（v8go 版）──────────────────────────────
// 翻译自 Python openai_sentinel_quickjs.cjs 的 main + dispatchBehavior。
// 结果写回 globalThis.__output（Go 侧读取）；超时/取消由 Go 侧 pump 循环控制。
//
// 注意：本文件与 shim.js 是两个独立 RunScript，顶层 const/let 是 script-scoped，
// 跨 script 共享的状态一律走 globalThis.__*（见 shim.js 末尾的挂载）。

function _rng(min, max) {
  return min + Math.floor(Math.random() * Math.max(1, max - min + 1));
}

async function dispatchBehavior(durationMs) {
  const started = Date.now();
  const _dispatch = globalThis.dispatch;
  const sleep = (min, max) => new Promise(r => setTimeout(r, _rng(min, max)));
  const moves = _rng(12, 16);
  let x = _rng(260, 420);
  let y = _rng(180, 300);
  for (let i = 0; i < moves; i++) {
    const dx = _rng(5, 18);
    const dy = _rng(-4, 12);
    x += dx;
    y += dy;
    await sleep(70, 145);
    await _dispatch('pointermove', {
      clientX: x, clientY: y, screenX: x, screenY: y,
      movementX: dx, movementY: dy, buttons: 0,
    });
  }
  await sleep(90, 220);
  await _dispatch('click', {
    clientX: x, clientY: y, screenX: x, screenY: y, button: 0, buttons: 0,
  });
  for (let i = 0; i < _rng(3, 4); i++) {
    await sleep(80, 180);
    globalThis.scrollY = (globalThis.scrollY || 0) + _rng(35, 120);
    globalThis.pageYOffset = globalThis.scrollY;
    await _dispatch('scroll', { scrollX: 0, scrollY: globalThis.scrollY });
  }
  await sleep(80, 160);
  await _dispatch('wheel', {
    deltaX: 0, deltaY: _rng(70, 140), clientX: x, clientY: y,
  });
  const keys = ['L', 'u', 'Tab'];
  for (const key of keys) {
    await sleep(90, 210);
    await _dispatch('keydown', {
      key,
      code: key === 'Tab' ? 'Tab' : 'Key' + key.toUpperCase(),
      repeat: false, altKey: false, ctrlKey: false, metaKey: false,
    });
  }
  const remaining = Math.max(0, Number(durationMs || 0) - (Date.now() - started));
  if (remaining > 0) await new Promise(r => setTimeout(r, remaining));
}

(async () => {
  const input = globalThis.__input || {};
  const action = input.action;
  const flow = String(input.flow || 'authorize_continue');

  if (action === 'requirements') {
    let requestP = null;
    try {
      await globalThis.SentinelSDK.init(flow);
      const cp = globalThis.__getCapturedProof ? globalThis.__getCapturedProof() : null;
      if (cp) requestP = cp;
    } catch (_) {}
    if (!requestP) {
      requestP = await globalThis.__debugP.getRequirementsToken();
    }
    globalThis.__output = { request_p: requestP };
    return;
  }

  if (action === 'solve') {
    const behaviorMs = Number(input.behavior_duration_ms || 4200);
    try {
      const mainToken = await globalThis.SentinelSDK.token(flow);
      if (mainToken) {
        await dispatchBehavior(behaviorMs);
        let soToken = '';
        try {
          soToken = await globalThis.SentinelSDK.sessionObserverToken(flow);
        } catch (_) {
          soToken = '';
        }
        globalThis.__output = { token: mainToken, so_token: soToken || '' };
        return;
      }
    } catch (_) {}

    // token() 不可用时的兜底路径（对齐 Python：__debugP.getEnforcementToken + bindProof）
    const challenge = input.challenge || {};
    const requestP = String(input.request_p || '').trim();
    if (!requestP) throw new Error('missing request_p');
    const finalP = await globalThis.__debugP.getEnforcementToken(challenge);
    if (typeof globalThis.SentinelSDK.__debug_bindProof === 'function') {
      globalThis.SentinelSDK.__debug_bindProof(challenge, requestP);
    }

    const dx = challenge && challenge.turnstile ? challenge.turnstile.dx : null;
    const tValue = (dx && typeof globalThis.SentinelSDK.__debug_n === 'function')
      ? await globalThis.SentinelSDK.__debug_n(challenge, dx)
      : null;
    globalThis.__output = { final_p: finalP, t: tValue, so_token: '' };
    return;
  }

  throw new Error('unsupported action: ' + action);
})().catch(err => {
  globalThis.__output = { error: String((err && err.stack) || err) };
});
