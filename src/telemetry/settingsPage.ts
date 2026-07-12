/**
 * SDK Features settings page — per-adapter toggle UI.
 * Same dark theme as the telemetry dashboard. No framework, no CDN.
 */

import { profileBarCss, profileBarHtml, profileBarJs, themeCss } from "./profileBar"
import { KEY_BOOTSTRAP } from "./keyBootstrap"

export const settingsPageHtml = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Meridian — SDK Features</title>
<link rel="icon" type="image/svg+xml" href="/telemetry/icon.svg">
<style>
  ${themeCss}
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif;
         background: var(--bg); color: var(--text); padding: 0; line-height: 1.5; }
  ${profileBarCss}
  .content { max-width: 900px; margin: 0 auto; padding: 24px; }
  h1 { font-size: 20px; font-weight: 600; margin-bottom: 4px; }
  .subtitle { color: var(--muted); font-size: 13px; margin-bottom: 24px; }
  .nav { display: flex; gap: 16px; margin-bottom: 24px; font-size: 13px; }
  .nav a { color: var(--muted); text-decoration: none; }
  .nav a:hover { color: var(--accent); }
  .nav a.active { color: var(--accent); }

  .adapter-card {
    background: var(--surface); border: 1px solid var(--border); border-radius: 8px;
    padding: 20px; margin-bottom: 16px;
  }
  .adapter-header {
    display: flex; align-items: center; justify-content: space-between;
    margin-bottom: 16px;
  }
  .adapter-name { font-size: 16px; font-weight: 600; }
  .adapter-badge {
    font-size: 10px; padding: 2px 8px; border-radius: 10px;
    text-transform: uppercase; letter-spacing: 0.5px;
  }
  .badge-active { background: rgba(63, 185, 80, 0.15); color: var(--green); }
  .badge-inactive { background: rgba(139, 148, 158, 0.15); color: var(--muted); }

  .feature-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
  @media (max-width: 600px) { .feature-grid { grid-template-columns: 1fr; } }

  .feature-row {
    display: flex; align-items: center; justify-content: space-between;
    padding: 10px 14px; border-radius: 6px;
    background: var(--bg); border: 1px solid var(--border);
  }
  .feature-info { display: flex; flex-direction: column; }
  .feature-label { font-size: 13px; font-weight: 500; }
  .feature-desc { font-size: 11px; color: var(--muted); margin-top: 2px; }

  /* Toggle switch */
  .toggle { position: relative; width: 36px; height: 20px; flex-shrink: 0; }
  .toggle input { opacity: 0; width: 0; height: 0; }
  .toggle-track {
    position: absolute; cursor: pointer; top: 0; left: 0; right: 0; bottom: 0;
    background: var(--border); border-radius: 10px; transition: background 0.2s;
  }
  .toggle-track::after {
    content: ""; position: absolute; height: 14px; width: 14px;
    left: 3px; bottom: 3px; background: var(--muted); border-radius: 50%;
    transition: transform 0.2s, background 0.2s;
  }
  .toggle input:checked + .toggle-track { background: var(--accent); }
  .toggle input:checked + .toggle-track::after {
    transform: translateX(16px); background: var(--text);
  }

  /* Select dropdown */
  .feature-select {
    background: var(--surface); color: var(--text); border: 1px solid var(--border);
    border-radius: 6px; padding: 4px 8px; font-size: 12px; cursor: pointer;
  }

  .save-indicator {
    position: fixed; bottom: 24px; right: 24px;
    background: var(--green); color: #000; padding: 8px 16px;
    border-radius: 6px; font-size: 13px; font-weight: 500;
    opacity: 0; transition: opacity 0.3s; pointer-events: none;
  }
  .save-indicator.visible { opacity: 1; }

  .reset-btn {
    background: none; border: 1px solid var(--border); color: var(--muted);
    border-radius: 6px; padding: 4px 12px; font-size: 11px; cursor: pointer;
  }
  .reset-btn:hover { border-color: var(--red); color: var(--red); }
</style>
</head>
<body>
<script>${KEY_BOOTSTRAP}</script>
${profileBarHtml}
<div class="content">
  <h1>SDK Features <span style="font-size:11px;padding:2px 8px;border-radius:10px;background:rgba(210,153,34,0.15);color:var(--yellow);vertical-align:middle;margin-left:8px">Experimental</span></h1>
  <p class="subtitle" style="max-width:720px;line-height:1.6">
    Unlock Claude Code features for any connected agent. Capabilities like auto-memory, dreaming, and CLAUDE.md — normally
    exclusive to Claude Code — become available to OpenCode, Crush, Droid, and any other harness routed through Meridian.
    Each agent keeps its own toolchain while gaining access to these additional features.<br><br>
    <strong style="color:var(--text)">System prompts:</strong> For these features to work correctly, both the Claude Code prompt and your client prompt
    should be enabled. When both are active, they are appended together — Claude Code's base instructions come first,
    followed by your agent's specific instructions.
  </p>

  <div class="adapter-card" style="margin-bottom:24px">
    <div class="adapter-header"><div class="adapter-name">🌐 出口代理 / Egress Proxy</div><span id="proxy-status" class="adapter-badge badge-inactive">未配置</span></div>
    <div style="font-size:12px;color:var(--muted);margin-bottom:10px">粘贴代理(支持 <code>socks5://host:port:user:pass</code> 或标准 URL),自动解析。出口流量(SDK 子进程 + native egress)将走此代理。</div>
    <textarea id="proxy-input" placeholder="socks5://23.148.60.245:9004:user:pass" style="width:100%;min-height:46px;background:var(--bg);color:var(--text);border:1px solid var(--border);border-radius:8px;padding:8px;font-size:12px;font-family:monospace"></textarea>
    <div id="proxy-parsed" style="font-size:12px;color:var(--muted);margin-top:8px"></div>
    <div style="display:flex;gap:10px;margin-top:10px">
      <button onclick="testProxy()" class="reset-btn" style="border-color:var(--accent);color:var(--accent)">🔌 测试代理</button>
      <button onclick="saveProxy()" class="reset-btn">💾 保存</button>
      <button onclick="clearProxy()" class="reset-btn">清除</button>
    </div>
    <div id="proxy-result" style="font-size:12px;margin-top:10px"></div>
  </div>

  <div id="adapters"></div>
</div>

<div class="save-indicator" id="saveIndicator">Saved</div>

<script>
const FEATURES = [
  { key: 'codeSystemPrompt', label: 'Claude Code Prompt', desc: 'Include the built-in Claude Code system prompt (tool usage rules, safety guidelines, coding best practices)', type: 'toggle' },
  { key: 'clientSystemPrompt', label: 'Client Prompt', desc: 'Include the system prompt sent by the connecting agent (e.g. OpenCode or Crush instructions)', type: 'toggle' },
  { key: 'claudeMd', label: 'CLAUDE.md', desc: 'Load CLAUDE.md instruction files — Off: none, Project: ./CLAUDE.md only, Full: ~/.claude/CLAUDE.md + ./CLAUDE.md', type: 'select', options: ['off', 'project', 'full'] },
  { key: 'memory', label: 'Memory', desc: 'Read and write memories across sessions', type: 'toggle' },
  { key: 'dreaming', label: 'Auto-Dream', desc: 'Background memory consolidation', type: 'toggle' },
  { key: 'thinking', label: 'Thinking', desc: 'Extended thinking mode', type: 'select', options: ['disabled', 'adaptive', 'enabled'] },
  { key: 'thinkingPassthrough', label: 'Thinking Passthrough', desc: 'Forward thinking blocks to the client', type: 'toggle' },
  { key: 'sharedMemory', label: 'Shared Memory', desc: 'Share memory with Claude Code (~/.claude) instead of isolated storage', type: 'toggle' },
  { key: 'maxBudgetUsd', label: 'Max Budget (USD)', desc: 'Per-request cost cap — query aborts if exceeded (0 = disabled)', type: 'number' },
  { key: 'fallbackModel', label: 'Fallback Model', desc: 'Auto-fallback model if primary fails', type: 'select', options: ['', 'sonnet', 'opus', 'haiku', 'sonnet[1m]', 'opus[1m]'] },
  { key: 'sdkDebug', label: 'SDK Debug Logging', desc: 'Enable verbose SDK debug output to proxy stderr', type: 'toggle' },
  { key: 'additionalDirectories', label: 'Additional Directories', desc: 'Comma-separated extra paths Claude can access (monorepo libs, etc.)', type: 'text' },
];

const ADAPTER_LABELS = {
  'claude-code': 'Claude Code',
  opencode: 'OpenCode',
  crush: 'Crush',
  forgecode: 'ForgeCode',
  pi: 'Pi',
  droid: 'Droid',
  passthrough: 'LiteLLM / Passthrough',
};

let currentConfig = {};
let globalNative = { nativeForward: true, nativeBodyCheck: false, injectSystemPrompt: false, injectTools: false };

async function loadGlobalNative() {
  const res = await fetch('/settings/api/native');
  globalNative = await res.json();
  renderGlobalNativeSection();
}

async function saveGlobalNative(key, value) {
  if (key === 'nativeForward' && value === true) {
    const ok = confirm('Native forwarding sends requests DIRECTLY to api.anthropic.com using your OAuth token, bypassing the SDK. This carries account risk — enabling it means all adapters with OAuth-capable profiles will bypass the SDK. Enable anyway?');
    if (!ok) { renderGlobalNativeSection(); return; }
  }
  const patch = {};
  patch[key] = value;
  await fetch('/settings/api/native', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
  globalNative[key] = value;
  showSaved();
}

function renderGlobalNativeSection() {
  const existing = document.getElementById('global-native-card');
  if (existing) existing.remove();
  const container = document.getElementById('adapters');
  const card = document.createElement('div');
  card.id = 'global-native-card';
  card.className = 'adapter-card';
  card.style.cssText = 'border-color: var(--accent); margin-bottom: 24px;';
  card.innerHTML =
    '<div class="adapter-header">' +
      '<span class="adapter-name">Native Forwarding ' +
        '<span style="font-size:11px;padding:2px 8px;border-radius:10px;background:rgba(210,153,34,0.15);color:var(--yellow);vertical-align:middle;margin-left:8px">Global</span>' +
      '</span>' +
    '</div>' +
    '<p style="font-size:12px;color:var(--muted);margin-bottom:12px">' +
      'Global override: applies to ALL adapters. When enabled, requests routed to OAuth-capable profiles bypass the Agent SDK and are forwarded verbatim to api.anthropic.com. ' +
      'Per-adapter nativeForward in the cards below still works as an additional enable.' +
    '</p>' +
    '<div class="feature-grid">' +
      '<div class="feature-row">' +
        '<div class="feature-info">' +
          '<span class="feature-label">Native Forwarding</span>' +
          '<span class="feature-desc">Bypass SDK globally — forward all OAuth-capable requests verbatim to api.anthropic.com</span>' +
        '</div>' +
        '<label class="toggle"><input type="checkbox" id="global-nativeForward" ' + (globalNative.nativeForward ? 'checked' : '') +
        ' onchange="saveGlobalNative(\\'nativeForward\\', this.checked)"><span class="toggle-track"></span></label>' +
      '</div>' +
      '<div class="feature-row">' +
        '<div class="feature-info">' +
          '<span class="feature-label">Anti-Forge Body Check</span>' +
          '<span class="feature-desc">Only forward natively when the body looks like genuine Claude Code (CC identity + tools). Keep ON.</span>' +
        '</div>' +
        '<label class="toggle"><input type="checkbox" id="global-nativeBodyCheck" ' + (globalNative.nativeBodyCheck ? 'checked' : '') +
        ' onchange="saveGlobalNative(\\'nativeBodyCheck\\', this.checked)"><span class="toggle-track"></span></label>' +
      '</div>' +
      '<div class="feature-row">' +
        '<div class="feature-info">' +
          '<span class="feature-label">Inject CC System Prompt</span>' +
          '<span class="feature-desc">Add the full Claude Code main system prompt (~7K tokens). OFF = cheap identity blocks only, so customer usage isn\\'t inflated by the ~33K envelope. Disguise vs cost.</span>' +
        '</div>' +
        '<label class="toggle"><input type="checkbox" id="global-injectSystemPrompt" ' + (globalNative.injectSystemPrompt ? 'checked' : '') +
        ' onchange="saveGlobalNative(\\'injectSystemPrompt\\', this.checked)"><span class="toggle-track"></span></label>' +
      '</div>' +
      '<div class="feature-row">' +
        '<div class="feature-info">' +
          '<span class="feature-label">Inject CC Base Tools</span>' +
          '<span class="feature-desc">Add the Claude Code 28 base tools (~26K tokens). OFF = pass the user\\'s OWN tools through verbatim (not the CC 28, not empty).</span>' +
        '</div>' +
        '<label class="toggle"><input type="checkbox" id="global-injectTools" ' + (globalNative.injectTools ? 'checked' : '') +
        ' onchange="saveGlobalNative(\\'injectTools\\', this.checked)"><span class="toggle-track"></span></label>' +
      '</div>' +
    '</div>';
  container.insertBefore(card, container.firstChild);
}

async function loadConfig() {
  const res = await fetch('/settings/api/features');
  currentConfig = await res.json();
  render();
}

async function saveFeature(adapter, key, value) {
  if (key === 'nativeForward' && value === true && adapter !== 'claude-code') {
    const ok = confirm('Native forwarding sends requests directly to api.anthropic.com using your OAuth token, bypassing the SDK. On non-Claude-Code adapters the tool/prompt shape differs from the real CLI, which carries a HIGHER risk of your account being flagged. Enable anyway?');
    if (!ok) { render(); return; }
  }
  const patch = {};
  patch[key] = value;
  await fetch('/settings/api/features/' + adapter, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
  currentConfig[adapter][key] = value;
  showSaved();
}

async function resetAdapter(adapter) {
  await fetch('/settings/api/features/' + adapter, { method: 'DELETE' });
  await loadConfig();
  showSaved();
}

function showSaved() {
  const el = document.getElementById('saveIndicator');
  el.classList.add('visible');
  setTimeout(() => el.classList.remove('visible'), 1500);
}

function hasAnyEnabled(features) {
  return features.codeSystemPrompt || !features.clientSystemPrompt || features.claudeMd !== 'off' || features.memory || features.dreaming ||
         features.thinking !== 'disabled' || features.thinkingPassthrough ||
         features.sharedMemory || features.maxBudgetUsd > 0 ||
         features.fallbackModel || features.sdkDebug ||
         features.additionalDirectories || features.nativeForward;
}

function render() {
  const container = document.getElementById('adapters');
  container.innerHTML = '';
  renderGlobalNativeSection();

  for (const [adapter, label] of Object.entries(ADAPTER_LABELS)) {
    const features = currentConfig[adapter] || {};
    const active = hasAnyEnabled(features);

    const card = document.createElement('div');
    card.className = 'adapter-card';
    card.innerHTML = '<div class="adapter-header">' +
      '<span class="adapter-name">' + label + '</span>' +
      '<div style="display:flex;gap:8px;align-items:center">' +
        '<span class="adapter-badge ' + (active ? 'badge-active' : 'badge-inactive') + '">' +
          (active ? 'Active' : 'Default') +
        '</span>' +
        '<button class="reset-btn" onclick="resetAdapter(\\''+adapter+'\\')">Reset</button>' +
      '</div>' +
    '</div>';

    const grid = document.createElement('div');
    grid.className = 'feature-grid';

    for (const feat of FEATURES) {
      const row = document.createElement('div');
      row.className = 'feature-row';

      const info = '<div class="feature-info"><span class="feature-label">' +
        feat.label + '</span><span class="feature-desc">' + feat.desc + '</span></div>';

      if (feat.type === 'toggle') {
        const checked = features[feat.key] ? 'checked' : '';
        row.innerHTML = info +
          '<label class="toggle"><input type="checkbox" ' + checked +
          ' onchange="saveFeature(\\''+adapter+'\\', \\''+feat.key+'\\', this.checked)">' +
          '<span class="toggle-track"></span></label>';
      } else if (feat.type === 'select') {
        const options = feat.options.map(o => {
          const label = o === '' ? '(None)' : o.charAt(0).toUpperCase()+o.slice(1);
          return '<option value="'+o+'"'+(features[feat.key]===o?' selected':'')+'>'+label+'</option>';
        }).join('');
        row.innerHTML = info +
          '<select class="feature-select" onchange="saveFeature(\\''+adapter+'\\', \\''+feat.key+'\\', this.value)">' +
          options + '</select>';
      } else if (feat.type === 'number') {
        const value = features[feat.key] ?? 0;
        row.innerHTML = info +
          '<input type="number" class="feature-select" style="width:80px;text-align:right" min="0" step="0.01" value="'+value+'"' +
          ' onchange="saveFeature(\\''+adapter+'\\', \\''+feat.key+'\\', parseFloat(this.value)||0)">';
      } else if (feat.type === 'text') {
        const value = (features[feat.key] ?? '').toString().replace(/"/g, '&quot;');
        row.innerHTML = info +
          '<input type="text" class="feature-select" style="width:180px" value="'+value+'"' +
          ' onchange="saveFeature(\\''+adapter+'\\', \\''+feat.key+'\\', this.value)">';
      }

      grid.appendChild(row);
    }

    card.appendChild(grid);
    container.appendChild(card);
  }
}

// --- Egress proxy UI ---
function parseProxyClient(raw){
  raw=(raw||'').trim(); if(!raw)return null;
  var scheme='socks5', m=raw.match(new RegExp('^(socks5h?|https?)://','i'));
  if(m){scheme=m[1].toLowerCase();raw=raw.slice(m[0].length);}
  var host='',port=NaN,user,pass;
  if(raw.indexOf('@')>=0){var at=raw.lastIndexOf('@');var auth=raw.slice(0,at);var hp=raw.slice(at+1);var ai=auth.indexOf(':');if(ai>=0){user=decodeURIComponent(auth.slice(0,ai));pass=decodeURIComponent(auth.slice(ai+1));}else if(auth){user=decodeURIComponent(auth);}var hpp=hp.split(':');host=hpp[0];port=parseInt(hpp[1],10);}
  else{var p=raw.split(':');if(p.length<2)return null;host=p[0];port=parseInt(p[1],10);if(p.length>=4){user=p[2];pass=p.slice(3).join(':');}else if(p.length===3){user=p[2];}}
  if(!host||!(port>0&&port<=65535))return null;
  return {scheme:scheme,host:host,port:port,user:user,pass:pass};
}
function maskPass(p){return p?(p.length<=2?'**':p[0]+'***'+p[p.length-1]):'';}
function renderProxyParsed(){
  var raw=document.getElementById('proxy-input').value;
  var p=parseProxyClient(raw); var el=document.getElementById('proxy-parsed');
  if(!raw){el.innerHTML='';return;}
  if(!p){el.innerHTML='<span style="color:var(--red)">无法解析(格式 scheme://host:port:user:pass)</span>';return;}
  el.innerHTML='✅ <b>'+p.scheme+'</b> · host <b>'+p.host+'</b> · port <b>'+p.port+'</b>'+(p.user?' · user <b>'+p.user+'</b>'+(p.pass?' · pass '+maskPass(p.pass):''):'');
}
async function testProxy(){
  var raw=document.getElementById('proxy-input').value.trim();
  var r=document.getElementById('proxy-result'); r.innerHTML='<span style="color:var(--muted)">测试中…(最多 12s)</span>';
  try{
    var res=await fetch('/settings/api/proxy/test',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({raw:raw})});
    var d=await res.json();
    if(d.ok){r.innerHTML='<span style="color:var(--green)">✅ 代理可用</span>'+(d.exitIp?' · 出口IP <b>'+d.exitIp+'</b>':'')+(d.latencyMs!=null?' · '+d.latencyMs+'ms':'');}
    else{r.innerHTML='<span style="color:var(--red)">❌ '+(d.error||'测试失败')+'</span>';}
  }catch(e){r.innerHTML='<span style="color:var(--red)">❌ '+e+'</span>';}
}
async function saveProxy(){
  var raw=document.getElementById('proxy-input').value.trim();
  var r=document.getElementById('proxy-result');
  try{
    var res=await fetch('/settings/api/proxy',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({raw:raw})});
    var d=await res.json();
    if(d.error){r.innerHTML='<span style="color:var(--red)">❌ '+d.error+'</span>';return;}
    r.innerHTML='<span style="color:var(--green)">✅ 已保存并应用</span>';
    var st=document.getElementById('proxy-status'); st.textContent=raw?'已启用':'未配置'; st.className='adapter-badge '+(raw?'badge-active':'badge-inactive');
    showSaved();
  }catch(e){r.innerHTML='<span style="color:var(--red)">❌ '+e+'</span>';}
}
function clearProxy(){document.getElementById('proxy-input').value='';renderProxyParsed();saveProxy();}
async function loadProxy(){
  try{var res=await fetch('/settings/api/proxy');var d=await res.json();
    if(d.proxy){document.getElementById('proxy-input').value=d.proxy;renderProxyParsed();var st=document.getElementById('proxy-status');st.textContent='已启用';st.className='adapter-badge badge-active';}
  }catch(e){}
}
document.getElementById('proxy-input').addEventListener('input',renderProxyParsed);
loadProxy();

loadConfig();
loadGlobalNative();
${profileBarJs}
</script>
</body>
</html>`
