/**
 * Meridian landing page.
 * Shows system status, account info, quick stats, and agent setup snippets.
 * Fetches /health and /telemetry/summary client-side for live data.
 */

import { profileBarCss, profileBarHtml, profileBarJs, themeCss } from "./profileBar"
import { KEY_BOOTSTRAP } from "./keyBootstrap"
import { WINDOW_LABELS } from "./profileUsage"

export const landingHtml = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Meridian</title>
<style>
  ${themeCss}
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif;
         background: var(--bg); color: var(--text); line-height: 1.6; min-height: 100vh; }
  .container { max-width: 960px; margin: 0 auto; padding: 32px 24px; }

  .header { display: flex; align-items: center; gap: 16px; margin-bottom: 6px; }
  .header h1 { font-size: 28px; font-weight: 700; letter-spacing: 3px; }
  .tagline { color: var(--muted); font-size: 14px; margin-bottom: 32px; letter-spacing: 0.5px; }

  .status-banner { display: flex; align-items: center; gap: 12px; padding: 16px 20px;
    background: var(--surface); border: 1px solid var(--border); border-radius: 12px; margin-bottom: 24px; }
  .status-dot { width: 12px; height: 12px; border-radius: 50%; flex-shrink: 0; }
  .status-dot.healthy { background: var(--green); box-shadow: 0 0 8px rgba(63,185,80,0.4); }
  .status-dot.degraded { background: var(--yellow); }
  .status-dot.unhealthy { background: var(--red); }
  .status-text { font-size: 14px; font-weight: 500; }
  .status-detail { font-size: 12px; color: var(--muted); margin-left: auto; }

  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 16px; margin-bottom: 24px; }
  .card { background: var(--surface); border: 1px solid var(--border); border-radius: 12px; padding: 20px; }
  .card-label { font-size: 11px; color: var(--muted); text-transform: uppercase; letter-spacing: 1px; font-weight: 500; }
  .card-value { font-size: 32px; font-weight: 700; margin-top: 4px; font-variant-numeric: tabular-nums; }
  .card-value.green { color: var(--green); }
  .card-value.violet { color: var(--violet); }
  .card-detail { font-size: 12px; color: var(--muted); margin-top: 4px; }

  .section { margin-bottom: 24px; }
  .section-title { font-size: 12px; font-weight: 600; color: var(--muted); text-transform: uppercase;
    letter-spacing: 1px; margin-bottom: 12px; }
  .info-grid { display: grid; grid-template-columns: 120px 1fr; gap: 8px 16px; font-size: 13px;
    background: var(--surface); border: 1px solid var(--border); border-radius: 12px; padding: 16px 20px; }
  .info-label { color: var(--muted); }
  .info-value { color: var(--text); font-family: 'SF Mono', SFMono-Regular, Consolas, monospace; font-size: 12px; }

  .snippet { background: var(--surface); border: 1px solid var(--border); border-radius: 12px;
    padding: 16px 20px; margin-top: 12px; }
  .snippet code { display: block; font-family: 'SF Mono', SFMono-Regular, Consolas, monospace;
    font-size: 12px; color: var(--lavender); line-height: 1.8; white-space: pre-wrap; word-break: break-all; }
  .snippet-tabs { display: flex; gap: 0; margin-bottom: 12px; }
  .snippet-tab { padding: 6px 14px; font-size: 11px; font-weight: 500; cursor: pointer;
    color: var(--muted); background: var(--surface); border: 1px solid var(--border); border-bottom: none; }
  .snippet-tab:first-child { border-radius: 8px 0 0 0; }
  .snippet-tab:last-child { border-radius: 0 8px 0 0; }
  .snippet-tab.active { color: var(--violet); background: var(--surface2); border-color: var(--accent); }

  .links { display: flex; gap: 12px; margin-top: 32px; flex-wrap: wrap; }
  .link { padding: 10px 20px; background: var(--surface2); border: 1px solid var(--border);
    border-radius: 8px; color: var(--violet); text-decoration: none; font-size: 13px; font-weight: 500;
    transition: border-color 0.2s; }
  .link:hover { border-color: var(--accent); }

  .footer { margin-top: 48px; padding-top: 24px; border-top: 1px solid var(--border);
    font-size: 11px; color: var(--muted); text-align: center; }
  .footer a { color: var(--violet); text-decoration: none; }
` + profileBarCss + `
</style>
</head>
<body>
<script>${KEY_BOOTSTRAP}</script>
` + profileBarHtml + `
<div class="container">
  <div class="header">
    <svg width="40" height="40" viewBox="0 0 64 64" fill="none" xmlns="http://www.w3.org/2000/svg">
      <rect width="64" height="64" rx="14" fill="#1C1830"/>
      <line x1="32" y1="10" x2="32" y2="54" stroke="#8B7CF6" stroke-width="2.5" stroke-linecap="round"/>
      <path d="M16 20 A18 18 0 0 1 48 20" fill="none" stroke="#C4B5FD" stroke-width="1.2" opacity="0.4"/>
      <path d="M16 44 A18 18 0 0 0 48 44" fill="none" stroke="#C4B5FD" stroke-width="1.2" opacity="0.4"/>
      <path d="M20 30 A14 14 0 0 1 44 30" fill="none" stroke="#C4B5FD" stroke-width="0.8" opacity="0.2"/>
      <path d="M20 34 A14 14 0 0 0 44 34" fill="none" stroke="#C4B5FD" stroke-width="0.8" opacity="0.2"/>
      <circle cx="32" cy="10" r="3.5" fill="#C4B5FD"/><circle cx="32" cy="54" r="3.5" fill="#C4B5FD"/>
      <circle cx="32" cy="32" r="3" fill="#8B7CF6"/>
    </svg>
    <h1>MERIDIAN</h1>
  </div>
  <div class="tagline">Harness Claude, your way.</div>
  <div id="content"><div style="color:var(--muted);padding:40px;text-align:center">Loading\u2026</div></div>
</div>
<script>
function ms(v){if(v==null||v===0)return '\u2014';return v<1000?v+'ms':(v/1000).toFixed(1)+'s'}
function card(l,v,d,c){return '<div class="card"><div class="card-label">'+l+'</div><div class="card-value '+(c||'')+'">'+v+'</div>'+(d?'<div class="card-detail">'+d+'</div>':'')+'</div>'}

var QWIN=${JSON.stringify(WINDOW_LABELS)};
function qLabel(t){if(QWIN[t])return QWIN[t];return String(t).split('_').map(function(p){return p?p[0].toUpperCase()+p.slice(1):p}).join(' ');}
function qReset(r){if(r==null||!isFinite(r))return'';var ms=r-Date.now();if(ms<=0)return'resetting';var m=Math.floor(ms/60000),h=Math.floor(m/60),d=Math.floor(h/24);if(d>0)return'resets '+d+'d '+(h%24)+'h';if(h>0)return'resets '+h+'h '+(m%60)+'m';return'resets '+m+'m';}

function renderNeedKey(){
  document.getElementById('content').innerHTML='<div style="padding:48px 24px;text-align:center"><div style="font-size:16px;color:var(--text);margin-bottom:10px">🔑 需要 API Key</div><div style="font-size:13px;color:var(--muted);line-height:1.7">在网址后加 <code style="color:var(--accent)">?key=&lt;你的 API_KEY&gt;</code> 再访问。<br>例如 <code style="color:var(--accent)">http://'+location.host+'/?key=sk-mrd-...</code></div></div>';
}
var lastHealth=null,lastStats=null,lastQuota=null;
// Initial render only — NO auto-refresh (no setInterval). quota is never fetched
// here; it (and a fresh health/stats) load ONLY when the user clicks 刷新.
async function refresh(){
  try{
    const hr=await fetch('/health');
    const sr=await fetch('/telemetry/summary?window=86400000');
    if(hr.status===401||sr.status===401){renderNeedKey();return;}
    lastHealth=hr.ok?await hr.json():{};
    lastStats=sr.ok?await sr.json():{};
    render(lastHealth,lastStats,lastQuota);
  }catch(e){document.getElementById('content').innerHTML='<div style="color:var(--red);padding:40px;text-align:center">Could not connect</div>'}
}
// Manual full refresh (health + stats + quota). Quota hits Anthropic's usage
// endpoint, so it is fetched ONLY on this explicit click — never automatically.
async function fetchQuota(){
  var btn=document.getElementById('quota-btn');if(btn){btn.textContent='刷新中…';btn.disabled=true;}
  try{
    const hr=await fetch('/health');const sr=await fetch('/telemetry/summary?window=86400000');
    if(hr.ok)lastHealth=await hr.json();
    if(sr.ok)lastStats=await sr.json();
    lastQuota=await fetch('/v1/usage/quota/all').then(function(r){return r.ok?r.json():null;}).catch(function(){return null;});
  }catch(e){}
  render(lastHealth||{},lastStats||{},lastQuota);
}

function render(h,s,q){
  h=h||{};s=s||{};
  const st=h.status||'unknown',dot=st==='healthy'?'healthy':st==='degraded'?'degraded':'unhealthy';
  let o='';
  o+='<div class="status-banner"><div class="status-dot '+dot+'"></div><span class="status-text">'+(st==='healthy'?'Operational':st==='degraded'?'Degraded':'Offline')+'</span><span class="status-detail">Port '+location.port+' \u00b7 '+(h.mode||'internal')+' mode</span></div>';
  const tr=s.totalRequests||0,ec=s.errorCount||0;
  const er=tr>0?((ec/tr)*100).toFixed(1):'0';
  o+='<div class="grid">'+card('Requests (24h)',tr,'','violet')+card('Median Response',ms(s.totalDuration?.p50),'p95: '+ms(s.totalDuration?.p95),'')+card('Median TTFB',ms(s.ttfb?.p50),'p95: '+ms(s.ttfb?.p95),'')+card('Error Rate',er+'%',ec+' errors',parseFloat(er)>5?'':'green')+'</div>';
  o+='<div class="section"><div class="section-title">Account</div>';
  if(h.auth?.loggedIn){o+='<div class="info-grid"><span class="info-label">Email</span><span class="info-value">'+(h.auth.email||'\u2014')+'</span><span class="info-label">Subscription</span><span class="info-value">'+(h.auth.subscriptionType||'\u2014')+'</span><span class="info-label">Mode</span><span class="info-value">'+(h.mode||'internal')+'</span><span class="info-label">Endpoint</span><span class="info-value">http://'+location.host+'</span></div>'}
  else{o+='<div class="info-grid"><span class="info-label">Status</span><span class="info-value" style="color:var(--yellow)">'+(h.error||'Not authenticated')+'</span></div>';
    o+='<div style="margin-top:16px;padding:16px;border:1px solid #2a2a3a;border-radius:10px">'
      +'<div style="font-weight:600;margin-bottom:10px">上号 / Onboard a Claude account</div>'
      +'<button onclick="mrdLoginUrl()" style="background:#8B7CF6;color:#fff;border:0;border-radius:8px;padding:8px 14px;cursor:pointer;font-size:13px">① 生成授权链接</button>'
      +'<div id="mrd-onb" style="margin-top:12px"></div>'
      +'</div>';}
  o+='</div>';
  var qp=(q&&q.profiles&&q.profiles.length)?q.profiles[0]:null;
  var qwin=qp&&qp.windows?qp.windows:[];
  o+='<div class="section"><div class="section-title">Rate Limits 限额 <button id="quota-btn" onclick="fetchQuota()" style="margin-left:8px;font-size:11px;padding:3px 12px;border:1px solid var(--accent);background:transparent;color:var(--accent);border-radius:6px;cursor:pointer;vertical-align:middle">🔄 刷新额度</button></div>';
  if(qwin.length){
    qwin.forEach(function(w){
      var u=(w.utilization!=null&&isFinite(w.utilization))?Math.max(0,Math.min(1,w.utilization)):null;
      var pct=u==null?'—':Math.round(u*100)+'%';
      var col=u==null?'var(--muted)':(u>=0.85?'var(--red)':(u>=0.6?'var(--yellow)':'var(--green)'));
      var rs=qReset(w.resetsAt);
      o+='<div style="margin-bottom:12px"><div style="display:flex;justify-content:space-between;align-items:baseline;font-size:12px;margin-bottom:4px"><span>'+qLabel(w.type)+(rs?' <span style="color:var(--muted)">· '+rs+'</span>':'')+'</span><span style="font-weight:600;color:'+col+'">'+pct+'</span></div><div style="height:6px;background:#22223a;border-radius:4px;overflow:hidden"><div style="height:100%;width:'+(u==null?0:Math.round(u*100))+'%;background:'+col+';transition:width .4s ease"></div></div></div>';
    });
  }else{
    var qe=qp&&qp.error;
    var qmsg=qe?(/^upstream_429$/.test(qe)?'— 限流中,稍后再试':(/^upstream_/.test(qe)?'— 上游错误':(qe==='fetch_failed'||qe==='refresh_failed'?'— 暂时不可用':(qe==='no_token'?'— 无 OAuth token':'— ('+qe+')')))):'点击「🔄 刷新额度」查看(实时请求 Anthropic,不自动刷新)';
    o+='<div style="color:var(--muted);font-size:12px">'+qmsg+'</div>';
  }
  o+='</div>';
  if(s.byModel&&Object.keys(s.byModel).length>0){o+='<div class="section"><div class="section-title">Models (24h)</div><div class="grid">';for(const[n,d]of Object.entries(s.byModel))o+=card(n,d.count,'avg '+ms(d.avgTotalMs),'');o+='</div></div>'}
  o+='<div class="section"><div class="section-title">Connect an Agent</div><div class="snippet"><div class="snippet-tabs"><div class="snippet-tab active" onclick="showTab(this,&apos;opencode&apos;)">OpenCode</div><div class="snippet-tab" onclick="showTab(this,&apos;crush&apos;)">Crush</div><div class="snippet-tab" onclick="showTab(this,&apos;generic&apos;)">Any Tool</div></div><div id="tab-opencode"><code>ANTHROPIC_API_KEY=x ANTHROPIC_BASE_URL=http://'+location.host+' opencode</code></div><div id="tab-crush" style="display:none"><code>'+JSON.stringify({providers:{meridian:{type:"anthropic",base_url:"http://"+location.host,api_key:"x",models:[{id:"claude-sonnet-4-5-20250514",name:"Sonnet 4.5"}]}}},null,2)+'</code></div><div id="tab-generic" style="display:none"><code>export ANTHROPIC_API_KEY=x\\nexport ANTHROPIC_BASE_URL=http://'+location.host+'</code></div></div></div>';
  o+='<div class="links"><a href="/telemetry" class="link">\ud83d\udcca Telemetry</a><a href="/settings" class="link">\ud83d\udd27 Settings</a><a href="/profiles" class="link">\ud83d\udc64 Profiles</a><a href="/health" class="link">\ud83e\ude7a Health</a><a href="/telemetry/summary" class="link">\ud83d\udcc8 Stats API</a><a href="https://github.com/rynfar/meridian" class="link">\u2699\ufe0f GitHub</a></div>';
  o+='<div class="footer">Meridian \u00b7 Built on the <a href="https://github.com/anthropics/claude-code-sdk-js">Claude Code SDK</a></div>';
  document.getElementById('content').innerHTML=o;
}
function showTab(el,id){document.querySelectorAll('.snippet-tab').forEach(t=>t.classList.remove('active'));el.classList.add('active');document.querySelectorAll('[id^="tab-"]').forEach(t=>t.style.display='none');document.getElementById('tab-'+id).style.display='block'}
var mrdSession=null;
async function mrdLoginUrl(){
  try{clearInterval(mrdTimer);}catch(e){}
  var box=document.getElementById('mrd-onb'); if(box)box.textContent='Generating…';
  try{
    var r=await fetch('/auth/login-url',{method:'POST'});
    if(!r.ok){if(box)box.innerHTML='<span style="color:var(--red)">'+(r.status===401?'需要在网址带 ?key=<API_KEY>':('Error '+r.status))+'</span>';return;}
    var d=await r.json();
    mrdSession={codeVerifier:d.codeVerifier,state:d.state};
    if(box)box.innerHTML='<div style="font-size:12px;color:var(--muted);margin-bottom:6px">用要上的账号登录 claude.com,打开此链接授权,把返回的授权码粘到下面:</div>'
      +'<a href="'+d.authorizeUrl+'" target="_blank" rel="noopener" style="color:#8B7CF6;word-break:break-all;font-size:12px">'+d.authorizeUrl+'</a>'
      +'<textarea id="mrd-code" placeholder="粘贴授权码 code#state" style="width:100%;margin-top:10px;min-height:54px;background:#0c0c14;color:#eee;border:1px solid #2a2a3a;border-radius:8px;padding:8px;font-size:12px"></textarea>'
      +'<button onclick="mrdExchange()" style="margin-top:8px;background:#22c55e;color:#06210f;border:0;border-radius:8px;padding:8px 14px;cursor:pointer;font-weight:600;font-size:13px">② 提交授权码</button>'
      +'<div id="mrd-msg" style="margin-top:8px;font-size:12px"></div>';
  }catch(e){if(box)box.innerHTML='<span style="color:var(--red)">'+e+'</span>';}
}
async function mrdExchange(){
  var msg=document.getElementById('mrd-msg'),ta=document.getElementById('mrd-code');
  if(!mrdSession||!ta||!ta.value.trim()){if(msg)msg.innerHTML='<span style="color:var(--yellow)">请先生成链接并粘贴授权码</span>';return;}
  if(msg)msg.textContent='Exchanging…';
  try{
    var r=await fetch('/auth/exchange',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({codeVerifier:mrdSession.codeVerifier,state:mrdSession.state,code:ta.value.trim()})});
    var d=await r.json().catch(function(){return{};});
    if(r.ok&&d.success){if(msg)msg.innerHTML='<span style="color:#22c55e">✓ 上号成功,刷新中…</span>';setTimeout(function(){location.reload();},1200);}
    else{if(msg)msg.innerHTML='<span style="color:var(--red)">'+(d.message||('Error '+r.status))+'</span>';}
  }catch(e){if(msg)msg.innerHTML='<span style="color:var(--red)">'+e+'</span>';}
}
refresh();var mrdTimer=null; // initial render only — no auto-refresh
` + profileBarJs + `
</script>
</body>
</html>`
