/**
 * Client-side API-key bootstrap for browser access to header-auth'd pages.
 *
 * When a page is opened with `?key=<API_KEY>`, this snippet:
 *   (a) monkeypatches window.fetch to append `key=` to every same-origin
 *       request, so the page's own API calls authenticate; and
 *   (b) rewrites in-page links (`<a href="/...">`) to carry the key, so
 *       navigation between admin pages stays authenticated.
 *
 * This lets a single `http://host:3456/?key=...` link reach Settings,
 * Telemetry, Profiles, etc. without a browser header-injection extension.
 *
 * Authored as a joined string array to avoid backticks / ${} that would
 * break the template literals this is interpolated into.
 */
export const KEY_BOOTSTRAP = [
  "(function(){",
  "var k=new URLSearchParams(location.search).get('key');",
  "if(!k)return;",
  "var of=window.fetch;",
  "window.fetch=function(input,init){try{",
  "var url=typeof input==='string'?input:(input&&input.url)||'';",
  "if(url&&(url.charAt(0)==='/'||url.indexOf(location.origin)===0)){",
  "var u=new URL(url,location.origin);",
  "if(!u.searchParams.get('key')){u.searchParams.set('key',k);",
  "input=(typeof input==='string')?(u.pathname+u.search):new Request(u.toString(),input);}}",
  "}catch(e){}return of.call(this,input,init);};",
  "function deco(){var as=document.querySelectorAll('a[href^=\"/\"]');for(var i=0;i<as.length;i++){try{var h=as[i].getAttribute('href');var u=new URL(h,location.origin);if(!u.searchParams.get('key')){u.searchParams.set('key',k);as[i].setAttribute('href',u.pathname+u.search);}}catch(e){}}}",
  "if(document.readyState!=='loading')deco();else document.addEventListener('DOMContentLoaded',deco);",
  "try{var mo=new MutationObserver(deco);mo.observe(document.documentElement,{childList:true,subtree:true});}catch(e){}",
  "})();",
].join("")
